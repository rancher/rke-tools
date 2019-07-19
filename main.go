package main

import (
	"archive/zip"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/minio/minio-go"
	"github.com/minio/minio-go/pkg/credentials"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	backupBaseDir       = "/backup"
	backupRetries       = 4
	compressedExtension = "zip"
	s3ServerRetries     = 3
	contentType         = "application/zip"
	ServerPort          = "2379"
	s3Endpoint          = "s3.amazonaws.com"
)

var commonFlags = []cli.Flag{
	cli.StringFlag{
		Name:  "endpoints",
		Usage: "Etcd endpoints",
		Value: "127.0.0.1:2379",
	},
	cli.BoolFlag{
		Name:   "debug",
		Usage:  "Verbose logging information for debugging purposes",
		EnvVar: "RANCHER_DEBUG",
	},
	cli.StringFlag{
		Name:  "name",
		Usage: "Backup name to take once",
	},
	cli.StringFlag{
		Name:   "cacert",
		Usage:  "Etcd CA client certificate path",
		EnvVar: "ETCD_CACERT",
	},
	cli.StringFlag{
		Name:   "cert",
		Usage:  "Etcd client certificate path",
		EnvVar: "ETCD_CERT",
	},
	cli.StringFlag{
		Name:   "key",
		Usage:  "Etcd client key path",
		EnvVar: "ETCD_KEY",
	},
	cli.StringFlag{
		Name:   "local-endpoint",
		Usage:  "Local backup download endpoint",
		EnvVar: "LOCAL_ENDPOINT",
	},
	cli.BoolFlag{
		Name:   "s3-backup",
		Usage:  "Backup etcd sanpshot to your s3 server, set true or false",
		EnvVar: "S3_BACKUP",
	},
	cli.StringFlag{
		Name:   "s3-endpoint",
		Usage:  "Specify s3 endpoint address",
		EnvVar: "S3_ENDPOINT",
	},
	cli.StringFlag{
		Name:   "s3-accessKey",
		Usage:  "Specify s3 access key",
		EnvVar: "S3_ACCESS_KEY",
	},
	cli.StringFlag{
		Name:   "s3-secretKey",
		Usage:  "Specify s3 secret key",
		EnvVar: "S3_SECRET_KEY",
	},
	cli.StringFlag{
		Name:   "s3-bucketName",
		Usage:  "Specify s3 bucket name",
		EnvVar: "S3_BUCKET_NAME",
	},
	cli.StringFlag{
		Name:   "s3-region",
		Usage:  "Specify s3 bucket region",
		EnvVar: "S3_BUCKET_REGION",
	},
	cli.StringFlag{
		Name:   "s3-endpoint-ca",
		Usage:  "Specify custom CA for S3 endpoint. Can be a file path or a base64 string",
		EnvVar: "S3_ENDPOINT_CA",
	},
	cli.StringFlag{
		Name:   "s3-folder",
		Usage:  "Specify folder for snapshots",
		EnvVar: "S3_FOLDER",
	},
}

type backupConfig struct {
	Backup     bool
	Endpoint   string
	AccessKey  string
	SecretKey  string
	BucketName string
	Region     string
	EndpointCA string
	Folder     string
}

func init() {
	log.SetOutput(os.Stderr)
}

func main() {
	err := os.Setenv("ETCDCTL_API", "3")
	if err != nil {
		log.Fatal(err)
	}

	app := cli.NewApp()
	app.Name = "Etcd Wrapper"
	app.Usage = "Utility services for Etcd cluster backup"
	app.Commands = []cli.Command{
		RollingBackupCommand(),
	}
	err = app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func RollingBackupCommand() cli.Command {

	snapshotFlags := []cli.Flag{
		cli.DurationFlag{
			Name:  "creation",
			Usage: "Create backups after this time interval in minutes",
			Value: 5 * time.Minute,
		},
		cli.DurationFlag{
			Name:  "retention",
			Usage: "Retain backups within this time interval in hours",
			Value: 24 * time.Hour,
		},
		cli.BoolFlag{
			Name:  "once",
			Usage: "Take backup only once",
		},
	}

	snapshotFlags = append(snapshotFlags, commonFlags...)

	return cli.Command{
		Name:  "etcd-backup",
		Usage: "Perform etcd backup tools",
		Subcommands: []cli.Command{
			{
				Name:   "save",
				Usage:  "Take snapshot on all etcd hosts and backup to s3 compatible storage",
				Flags:  snapshotFlags,
				Action: RollingBackupAction,
			},
			{
				Name:   "download",
				Usage:  "Download specified snapshot from s3 compatible storage or another local endpoint",
				Flags:  commonFlags,
				Action: DownloadBackupAction,
			},
			{
				Name:  "serve",
				Usage: "Provide HTTPS endpoint to pull local snapshot",
				Flags: []cli.Flag{
					cli.StringFlag{
						Name:  "name",
						Usage: "Backup name to take once",
					},
					cli.StringFlag{
						Name:   "cacert",
						Usage:  "Etcd CA client certificate path",
						EnvVar: "ETCD_CACERT",
					},
					cli.StringFlag{
						Name:   "cert",
						Usage:  "Etcd client certificate path",
						EnvVar: "ETCD_CERT",
					},
					cli.StringFlag{
						Name:   "key",
						Usage:  "Etcd client key path",
						EnvVar: "ETCD_KEY",
					},
				},
				Action: ServeBackupAction,
			},
		},
	}
}

func SetLoggingLevel(debug bool) {
	if debug {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}
}

func RollingBackupAction(c *cli.Context) error {
	SetLoggingLevel(c.Bool("debug"))

	creationPeriod := c.Duration("creation")
	retentionPeriod := c.Duration("retention")
	etcdCert := c.String("cert")
	etcdCACert := c.String("cacert")
	etcdKey := c.String("key")
	etcdEndpoints := c.String("endpoints")
	if creationPeriod == 0 || retentionPeriod == 0 {
		log.WithFields(log.Fields{
			"creation":  creationPeriod,
			"retention": retentionPeriod,
		}).Errorf("Creation period and/or retention are not set")
		return fmt.Errorf("Creation period and/or retention are not set")
	}

	if len(etcdCert) == 0 || len(etcdCACert) == 0 || len(etcdKey) == 0 {
		log.WithFields(log.Fields{
			"etcdCert":   etcdCert,
			"etcdCACert": etcdCACert,
			"etcdKey":    etcdKey,
		}).Errorf("Failed to find etcd cert or key paths")
		return fmt.Errorf("Failed to find etcd cert or key paths")
	}

	log.WithFields(log.Fields{
		"creation":  creationPeriod,
		"retention": retentionPeriod,
	}).Info("Initializing Rolling Backups")
	s3Backup := c.Bool("s3-backup")
	bc := &backupConfig{
		Backup:     s3Backup,
		Endpoint:   c.String("s3-endpoint"),
		AccessKey:  c.String("s3-accessKey"),
		SecretKey:  c.String("s3-secretKey"),
		BucketName: c.String("s3-bucketName"),
		Region:     c.String("s3-region"),
		EndpointCA: c.String("s3-endpoint-ca"),
		Folder:     c.String("s3-folder"),
	}

	var client = &minio.Client{}
	var tr = http.DefaultTransport
	var err error
	if s3Backup {
		client, err = setS3Service(bc, true)
		if err != nil {
			log.WithFields(log.Fields{
				"s3-endpoint":    bc.Endpoint,
				"s3-bucketName":  bc.BucketName,
				"s3-accessKey":   bc.AccessKey,
				"s3-region":      bc.Region,
				"s3-endpoint-ca": bc.EndpointCA,
				"s3-folder":      bc.Folder,
			}).Errorf("failed to set s3 server: %s", err)
			return fmt.Errorf("failed to set s3 server: %+v", err)
		}
		if bc.EndpointCA != "" {
			tr, err = setTransportCA(tr, bc.EndpointCA)
			if err != nil {
				return err
			}
		}
		client.SetCustomTransport(tr)
	}

	if c.Bool("once") {
		backupName := c.String("name")
		if err := CreateBackup(backupName, etcdCACert, etcdCert, etcdKey, etcdEndpoints, client, bc); err != nil {
			return err
		}
		prefix := getNamePrefix(backupName)
		// we only clean named backups if we have a retention period and a cluster name prefix
		if retentionPeriod != 0 && len(prefix) != 0 {
			if err := DeleteNamedBackups(retentionPeriod, prefix); err != nil {
				return err
			}
		}
		return nil
	}
	backupTicker := time.NewTicker(creationPeriod)
	for {
		select {
		case backupTime := <-backupTicker.C:
			backupName := fmt.Sprintf("%s_etcd", backupTime.Format(time.RFC3339))
			if err := CreateBackup(backupName, etcdCACert, etcdCert, etcdKey, etcdEndpoints, client, bc); err == nil {
				DeleteBackups(backupTime, retentionPeriod)
				if s3Backup {
					DeleteS3Backups(backupTime, retentionPeriod, client, bc)
				}
			}
		}
	}
}

func CreateBackup(backupName string, etcdCACert, etcdCert, etcdKey, endpoints string, svc *minio.Client, server *backupConfig) error {
	failureInterval := 15 * time.Second
	backupDir := fmt.Sprintf("%s/%s", backupBaseDir, backupName)
	var err error
	for retries := 0; retries <= backupRetries; retries++ {
		if retries > 0 {
			time.Sleep(failureInterval)
		}
		// check if the cluster is healthy
		cmd := exec.Command("etcdctl",
			fmt.Sprintf("--endpoints=%s", endpoints),
			"--cacert="+etcdCACert,
			"--cert="+etcdCert,
			"--key="+etcdKey,
			"endpoint", "health")
		data, err := cmd.CombinedOutput()

		if strings.Contains(string(data), "unhealthy") {
			log.WithFields(log.Fields{
				"error": err,
				"data":  string(data),
			}).Warn("Checking member health failed from etcd member")
			continue
		}

		cmd = exec.Command("etcdctl",
			fmt.Sprintf("--endpoints=%s", endpoints),
			"--cacert="+etcdCACert,
			"--cert="+etcdCert,
			"--key="+etcdKey,
			"snapshot", "save", backupDir)

		startTime := time.Now()
		data, err = cmd.CombinedOutput()
		endTime := time.Now()

		if err != nil {
			log.WithFields(log.Fields{
				"attempt": retries + 1,
				"error":   err,
				"data":    string(data),
			}).Warn("Backup failed")
			continue
		}
		// Compress backup
		compressedFilePath, err := compressFile(backupDir)
		if err != nil {
			log.WithFields(log.Fields{
				"attempt": retries + 1,
				"error":   err,
				"data":    string(data),
			}).Warn("Compressing backup failed")
			continue
		}
		// Set the full path to the compressed file for uploading
		compressedFile := fmt.Sprintf("%s.%s", backupName, compressedExtension)
		// Remove the original file after succesfully compressing it
		err = os.Remove(backupDir)
		if err != nil {
			log.WithFields(log.Fields{
				"attempt": retries + 1,
				"error":   err,
				"data":    string(data),
			}).Warn("Removing uncompressed snapshot file failed")

		}

		log.WithFields(log.Fields{
			"name":    backupName,
			"runtime": endTime.Sub(startTime),
		}).Info("Created backup")

		if err := os.Chmod(compressedFilePath, 0600); err != nil {
			log.WithFields(log.Fields{
				"attempt": retries + 1,
				"error":   err,
				"data":    string(data),
			}).Warn("changing permission of the compressed snapshot failed")
		}

		if server.Backup {
			// If folder is specified, prefix the file with the folder
			if len(server.Folder) != 0 {
				compressedFile = fmt.Sprintf("%s/%s", server.Folder, compressedFile)
			}
			err = uploadBackupFile(svc, server.BucketName, compressedFile, compressedFilePath)
			if err == nil {
				return nil
			}
		}
		break
	}
	return err
}

func DeleteBackups(backupTime time.Time, retentionPeriod time.Duration) {
	files, err := ioutil.ReadDir(backupBaseDir)
	if err != nil {
		log.WithFields(log.Fields{
			"dir":   backupBaseDir,
			"error": err,
		}).Warn("Can't read backup directory")
	}

	cutoffTime := backupTime.Add(retentionPeriod * -1)

	for _, file := range files {
		if file.IsDir() {
			log.WithFields(log.Fields{
				"name": file.Name(),
			}).Warn("Ignored directory, expecting file")
			continue
		}

		backupTime, err2 := time.Parse(time.RFC3339, strings.Split(file.Name(), "_")[0])
		if err2 != nil {
			log.WithFields(log.Fields{
				"name":  file.Name(),
				"error": err2,
			}).Warn("Couldn't parse backup")

		} else if backupTime.Before(cutoffTime) {
			_ = DeleteBackup(file)
		}
	}
}

func DeleteBackup(file os.FileInfo) error {
	toDelete := fmt.Sprintf("%s/%s", backupBaseDir, file.Name())

	cmd := exec.Command("rm", "-r", toDelete)

	startTime := time.Now()
	err2 := cmd.Run()
	endTime := time.Now()

	if err2 != nil {
		log.WithFields(log.Fields{
			"name":  file.Name(),
			"error": err2,
		}).Warn("Delete backup failed")
		return err2
	}
	log.WithFields(log.Fields{
		"name":    file.Name(),
		"runtime": endTime.Sub(startTime),
	}).Info("Deleted backup")
	return nil
}

func DeleteS3Backups(backupTime time.Time, retentionPeriod time.Duration, svc *minio.Client, bc *backupConfig) {
	log.WithFields(log.Fields{
		"retention": retentionPeriod,
	}).Info("Invoking delete s3 backup files")
	var backupDeleteList []string

	cutoffTime := backupTime.Add(retentionPeriod * -1)

	// Create a done channel to control 'ListObjectsV2' go routine.
	doneCh := make(chan struct{})

	// Indicate to our routine to exit cleanly upon return.
	defer close(doneCh)

	isRecursive := false
	prefix := ""
	if len(bc.Folder) != 0 {
		prefix = bc.Folder
	}
	objectCh := svc.ListObjects(bc.BucketName, prefix, isRecursive, doneCh)
	re := regexp.MustCompile(fmt.Sprintf(".+_etcd(|.%s)$", compressedExtension))
	for object := range objectCh {
		if object.Err != nil {
			log.Error("error to fetch s3 file:", object.Err)
			return
		}
		// only parse backup file names that matches *_etcd format
		if re.MatchString(object.Key) {
			backupTime, err := time.Parse(time.RFC3339, strings.Split(object.Key, "_")[0])
			if err != nil {
				log.WithFields(log.Fields{
					"name":  object.Key,
					"error": err,
				}).Warn("Couldn't parse s3 backup")

			} else if backupTime.Before(cutoffTime) {
				backupDeleteList = append(backupDeleteList, object.Key)
			}
		}
	}

	for i := range backupDeleteList {
		log.Infof("Start to delete s3 backup file [%s]", backupDeleteList[i])
		err := svc.RemoveObject(bc.BucketName, backupDeleteList[i])
		if err != nil {
			log.Errorf("Error detected during deletion: %v", err)
		} else {
			log.Infof("Success delete s3 backup file [%s]", backupDeleteList[i])
		}
	}
}

func setS3Service(bc *backupConfig, useSSL bool) (*minio.Client, error) {
	// Initialize minio client object.
	log.Info("invoking set s3 service client")
	var err error
	var client = &minio.Client{}
	var cred = &credentials.Credentials{}
	var tr = http.DefaultTransport
	bucketLookup := getBucketLookupType(bc.Endpoint)
	for retries := 0; retries <= s3ServerRetries; retries++ {
		// if the s3 access key and secret is not set use iam role
		if len(bc.AccessKey) == 0 && len(bc.SecretKey) == 0 {
			log.Info("invoking set s3 service client use IAM role")
			cred = credentials.NewIAM("")
			if bc.Endpoint == "" {
				bc.Endpoint = s3Endpoint
			}
		} else {
			cred = credentials.NewStatic(bc.AccessKey, bc.SecretKey, "", credentials.SignatureDefault)
		}
		client, err = minio.NewWithOptions(bc.Endpoint, &minio.Options{
			Creds:        cred,
			Secure:       useSSL,
			Region:       bc.Region,
			BucketLookup: bucketLookup,
		})
		if err != nil {
			log.Infof("failed to init s3 client server: %v, retried %d times", err, retries)
			if retries >= s3ServerRetries {
				return nil, fmt.Errorf("failed to set s3 server: %v", err)
			}
			continue
		}
		if bc.EndpointCA != "" {
			tr, err = setTransportCA(tr, bc.EndpointCA)
			if err != nil {
				return nil, err
			}
		}
		client.SetCustomTransport(tr)

		break
	}

	found, err := client.BucketExists(bc.BucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to check s3 bucket:%s, err:%v", bc.BucketName, err)
	}
	if !found {
		return nil, fmt.Errorf("bucket %s is not found", bc.BucketName)
	}
	return client, nil
}

func getBucketLookupType(endpoint string) minio.BucketLookupType {
	if endpoint == "" {
		return minio.BucketLookupAuto
	}
	if strings.Contains(endpoint, "aliyun") {
		return minio.BucketLookupDNS
	}
	return minio.BucketLookupAuto
}

func uploadBackupFile(svc *minio.Client, bucketName, fileName, filePath string) error {
	// Upload the zip file with FPutObject
	log.Infof("invoking uploading backup file %s to s3", fileName)
	for retries := 0; retries <= s3ServerRetries; retries++ {
		n, err := svc.FPutObject(bucketName, fileName, filePath, minio.PutObjectOptions{ContentType: contentType})
		if err != nil {
			log.Infof("failed to upload etcd snapshot file: %v, retried %d times", err, retries)
			if retries >= s3ServerRetries {
				return fmt.Errorf("failed to upload etcd snapshot file: %v", err)
			}
			continue
		}
		log.Infof("Successfully uploaded %s of size %d\n", fileName, n)
		break
	}
	return nil
}

func DownloadBackupAction(c *cli.Context) error {
	log.Info("Initializing Download Backups")
	SetLoggingLevel(c.Bool("debug"))
	if c.Bool("s3-backup") {
		return DownloadS3Backup(c)
	}
	return DownloadLocalBackup(c)
}

func DownloadS3Backup(c *cli.Context) error {
	bc := &backupConfig{
		Endpoint:   c.String("s3-endpoint"),
		AccessKey:  c.String("s3-accessKey"),
		SecretKey:  c.String("s3-secretKey"),
		BucketName: c.String("s3-bucketName"),
		Region:     c.String("s3-region"),
		EndpointCA: c.String("s3-endpoint-ca"),
		Folder:     c.String("s3-folder"),
	}
	client, err := setS3Service(bc, true)
	if err != nil {
		log.WithFields(log.Fields{
			"s3-endpoint":    bc.Endpoint,
			"s3-bucketName":  bc.BucketName,
			"s3-accessKey":   bc.AccessKey,
			"s3-region":      bc.Region,
			"s3-endpoint-ca": bc.EndpointCA,
			"s3-folder":      bc.Folder,
		}).Errorf("failed to set s3 server: %s", err)
		return fmt.Errorf("failed to set s3 server: %+v", err)
	}

	prefix := c.String("name")
	if len(prefix) == 0 {
		return fmt.Errorf("empty backup name")
	}
	folder := c.String("s3-folder")
	if len(folder) != 0 {
		prefix = fmt.Sprintf("%s/%s", folder, prefix)
	}
	// we need download with prefix because we don't know if the file is ziped or not
	filename, err := downloadFromS3WithPrefix(client, prefix, bc.BucketName)
	if err != nil {
		return err
	}
	if isCompressed(filename) {
		log.Infof("Decompressing etcd snapshot file [%s]", filename)
		compressedFileLocation := fmt.Sprintf("%s/%s", backupBaseDir, filename)
		fileLocation := fmt.Sprintf("%s/%s", backupBaseDir, decompressedName(filename))
		err := decompressFile(compressedFileLocation, fileLocation)
		if err != nil {
			return fmt.Errorf("Unable to decompress [%s] to [%s]: %v", compressedFileLocation, fileLocation, err)
		}
		if err := os.Chmod(fileLocation, 0600); err != nil {
			log.WithFields(log.Fields{
				"error": err,
			}).Warn("changing permission of the decompressed snapshot failed")
		}
		log.Infof("Decompressed [%s] to [%s]", compressedFileLocation, fileLocation)
	}
	return nil
}

func DownloadLocalBackup(c *cli.Context) error {
	snapshot := path.Base(c.String("name"))
	endpoint := c.String("local-endpoint")
	if snapshot == "." || snapshot == "/" {
		return fmt.Errorf("snapshot name is required")
	}
	if len(endpoint) == 0 {
		return fmt.Errorf("local-endpoint is required")
	}
	certs, err := getCertsFromCli(c)
	if err != nil {
		return err
	}
	tlsConfig, err := setupTLSConfig(certs, false)
	if err != nil {
		return err
	}
	client := http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}

	snapshotURL := fmt.Sprintf("https://%s:%s/%s", endpoint, ServerPort, snapshot)
	log.Infof("Invoking downloading backup files: %s", snapshot)
	log.Infof("Trying to download backup file from: %s", snapshotURL)
	resp, err := client.Get(snapshotURL)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		log.Errorf("backup download failed: %v", resp.Body)
		return fmt.Errorf("backup download failed: %v", resp.Body)
	}
	defer resp.Body.Close()

	snapshotFileLocation := fmt.Sprintf("%s/%s", backupBaseDir, snapshot)
	snapshotFile, err := os.Create(snapshotFileLocation)
	if err != nil {
		return err
	}
	defer snapshotFile.Close()

	if _, err := io.Copy(snapshotFile, resp.Body); err != nil {
		return err
	}

	if err := os.Chmod(snapshotFileLocation, 0600); err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Warn("changing permission of the locally downloaded snapshot failed")
	}

	log.Infof("Successfully download %s from %s ", snapshot, endpoint)
	return nil
}

func DeleteNamedBackups(retentionPeriod time.Duration, prefix string) error {
	files, err := ioutil.ReadDir(backupBaseDir)
	if err != nil {
		log.WithFields(log.Fields{
			"dir":   backupBaseDir,
			"error": err,
		}).Warn("Can't read backup directory")
		return err
	}
	cutoffTime := time.Now().Add(retentionPeriod * -1)
	for _, file := range files {
		if strings.HasPrefix(file.Name(), prefix) && file.ModTime().Before(cutoffTime) && IsRecurringSnapshot(file.Name()) {
			if err = DeleteBackup(file); err != nil {
				return err
			}
		}
	}
	return nil
}

func getNamePrefix(name string) string {
	re := regexp.MustCompile("^c-[a-z0-9].*?-")
	m := re.FindStringSubmatch(name)
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

func ServeBackupAction(c *cli.Context) error {
	snapshot := path.Base(c.String("name"))

	if snapshot == "." || snapshot == "/" {
		return fmt.Errorf("snapshot name is required")
	}
	// Check if snapshot is compressed
	compressedFileLocation := fmt.Sprintf("%s/%s.%s", backupBaseDir, snapshot, compressedExtension)
	if _, err := os.Stat(compressedFileLocation); err == nil {
		err := decompressFile(compressedFileLocation, fmt.Sprintf("%s/%s", backupBaseDir, snapshot))
		if err != nil {
			return err
		}
		log.Infof("Extracted from %s", compressedFileLocation)
	}

	if _, err := os.Stat(fmt.Sprintf("%s/%s", backupBaseDir, snapshot)); err != nil {
		return err
	}
	certs, err := getCertsFromCli(c)
	if err != nil {
		return err
	}
	tlsConfig, err := setupTLSConfig(certs, true)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:      fmt.Sprintf("0.0.0.0:%s", ServerPort),
		TLSConfig: tlsConfig,
	}

	http.HandleFunc(fmt.Sprintf("/%s", snapshot), func(response http.ResponseWriter, request *http.Request) {
		http.ServeFile(response, request, fmt.Sprintf("%s/%s", backupBaseDir, snapshot))
	})
	return httpServer.ListenAndServeTLS(certs["cert"], certs["key"])
}

func getCertsFromCli(c *cli.Context) (map[string]string, error) {
	caCert := c.String("cacert")
	cert := c.String("cert")
	key := c.String("key")
	if len(cert) == 0 || len(caCert) == 0 || len(key) == 0 {
		return nil, fmt.Errorf("cacert, cert and key are required")
	}

	return map[string]string{"cacert": caCert, "cert": cert, "key": key}, nil
}

func setupTLSConfig(certs map[string]string, isServer bool) (*tls.Config, error) {
	caCertPem, err := ioutil.ReadFile(certs["cacert"])
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{}
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(caCertPem)
	if isServer {
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		tlsConfig.ClientCAs = certPool
		tlsConfig.MinVersion = tls.VersionTLS12
	} else { // client config
		x509Pair, err := tls.LoadX509KeyPair(certs["cert"], certs["key"])
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{x509Pair}
		tlsConfig.RootCAs = certPool
		// This is to avoid IP SAN errors.
		tlsConfig.InsecureSkipVerify = true
	}

	tlsConfig.BuildNameToCertificate()
	return tlsConfig, nil
}

func IsRecurringSnapshot(name string) bool {
	// name is fmt.Sprintf("%s-%s%s-", cluster.Name, typeFlag, providerFlag)
	// typeFlag = "r": recurring
	// typeFlag = "m": manaul
	//
	// providerFlag = "l" local
	// providerFlag = "s" s3
	re := regexp.MustCompile("^c-[a-z0-9].*?-r.-")
	return re.MatchString(name)
}

func downloadFromS3WithPrefix(client *minio.Client, prefix, bucket string) (string, error) {
	var filename string
	doneCh := make(chan struct{})
	defer close(doneCh)

	objectCh := client.ListObjectsV2(bucket, prefix, false, doneCh)
	for object := range objectCh {
		if object.Err != nil {
			log.Error("failed to list objects in backup buckets [%s]:", bucket, object.Err)
			return "", object.Err
		}
		if prefix == decompressedName(object.Key) {
			filename = object.Key
			break
		}
	}
	if len(filename) == 0 {
		return "", fmt.Errorf("failed to download s3 backup: no backups found")
	}
	// if folder is included, strip it so it doesnt end up in a folder on the host itself
	targetFilename := path.Base(filename)
	for retries := 0; retries <= s3ServerRetries; retries++ {
		err := client.FGetObject(bucket, filename, backupBaseDir+"/"+targetFilename, minio.GetObjectOptions{})
		if err != nil {
			log.Infof("Failed to download etcd snapshot file [%s]: %v, retried %d times", filename, err, retries)
			if retries >= s3ServerRetries {
				log.Warningf("Failed to download etcd snapshot file [%s]: %v", filename, err)
				break
			}
		}
		log.Infof("Successfully downloaded [%s]", filename)
		return targetFilename, nil
	}
	return "", fmt.Errorf("Unable to download backup file for [%s]", filename)
}

func compressFile(fileName string) (string, error) {
	// Check if source file exists
	fileToZip, err := os.Open(fileName)
	if err != nil {
		return "", err
	}
	defer fileToZip.Close()

	// Create destination file
	compressedFile := fmt.Sprintf("%s.%s", fileName, compressedExtension)
	zipFile, err := os.Create(compressedFile)
	if err != nil {
		return "", err
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	header := &zip.FileHeader{
		Name:   fileName,
		Method: zip.Deflate,
	}

	header.SetModTime(time.Unix(0, 0))

	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(writer, fileToZip)
	if err != nil {
		return "", err
	}
	return compressedFile, nil
}

// decompressFile: Thanks to https://golangcode.com/unzip-files-in-go/
func decompressFile(src string, dest string) error {

	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {

		outFile, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		_, err = io.Copy(outFile, rc)

		// Close the file without defer to close before next iteration of loop
		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}
	}
	return nil
}

func readZipFile(zf *zip.File) ([]byte, error) {
	f, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ioutil.ReadAll(f)
}

func readS3EndpointCA(endpointCA string) ([]byte, error) {
	// I expect the CA to be passed as base64 string OR a file system path.
	// I do this to be able to pass it through rke/rancher api without writing it
	// to the backup container filesystem.
	ca, err := base64.StdEncoding.DecodeString(endpointCA)
	if err == nil {
		log.Debug("reading s3-endpoint-ca as a base64 string")
	} else {
		ca, err = ioutil.ReadFile(endpointCA)
		log.Debugf("reading s3-endpoint-ca from [%v]", endpointCA)
	}
	return ca, err
}

func isValidCertificate(c []byte) bool {
	p, _ := pem.Decode(c)
	if p == nil {
		return false
	}
	_, err := x509.ParseCertificates(p.Bytes)
	if err != nil {
		return false
	}
	return true
}

func setTransportCA(tr http.RoundTripper, endpointCA string) (http.RoundTripper, error) {
	ca, err := readS3EndpointCA(endpointCA)
	if err != nil {
		return tr, err
	}
	if !isValidCertificate(ca) {
		return tr, fmt.Errorf("s3-endpoint-ca is not a valid x509 certificate")
	}
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(ca)

	tr.(*http.Transport).TLSClientConfig = &tls.Config{
		RootCAs: certPool,
	}

	return tr, nil
}

func isCompressed(filename string) bool {
	return strings.HasSuffix(filename, fmt.Sprintf(".%s", compressedExtension))
}

func decompressedName(filename string) string {
	return strings.TrimSuffix(filename, path.Ext(filename))
}
