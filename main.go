package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/minio/minio-go"
	"github.com/minio/minio-go/pkg/credentials"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	backupBaseDir   = "/backup"
	backupRetries   = 4
	s3ServerRetries = 3
	contentType     = "application/zip"
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
}

type backupConfig struct {
	Backup     bool
	Endpoint   string
	AccessKey  string
	SecretKey  string
	BucketName string
	Region     string
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
				Usage:  "Download specified snapshot from s3 storage server",
				Flags:  commonFlags,
				Action: DownloadBackupAction,
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
	}

	client := &minio.Client{}
	if s3Backup {
		svc, err := setS3Service(bc, true)
		if err != nil {
			log.WithFields(log.Fields{
				"s3-endpoint":   bc.Endpoint,
				"s3-bucketName": bc.BucketName,
				"s3-accessKey":  bc.AccessKey,
				"s3-region":     bc.Region,
			}).Errorf("failed to set s3 server: %s", err)
			return fmt.Errorf("faield to set s3 server: %+v", err)
		}
		client = svc
	}

	if c.Bool("once") {
		backupName := c.String("name")
		if len(backupName) == 0 {
			backupName = fmt.Sprintf("%s_etcd", time.Now().Format(time.RFC3339))
		}
		return CreateBackup(backupName, etcdCACert, etcdCert, etcdKey, etcdEndpoints, client, bc)
	}
	backupTicker := time.NewTicker(creationPeriod)
	for {
		select {
		case backupTime := <-backupTicker.C:
			backupName := fmt.Sprintf("%s_etcd", backupTime.Format(time.RFC3339))
			CreateBackup(backupName, etcdCACert, etcdCert, etcdKey, etcdEndpoints, client, bc)
			DeleteBackups(backupTime, retentionPeriod)
			if s3Backup {
				DeleteS3Backups(backupTime, retentionPeriod, client, bc)
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
			fmt.Sprintf("--endpoints=[%s]", endpoints),
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
			fmt.Sprintf("--endpoints=[%s]", endpoints),
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
		log.WithFields(log.Fields{
			"name":    backupName,
			"runtime": endTime.Sub(startTime),
		}).Info("Created backup")

		if server.Backup {
			err := uploadBackupFile(svc, server.BucketName, backupName, backupDir)
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
			DeleteBackup(file)
		}
	}
}

func DeleteBackup(file os.FileInfo) {
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

	} else {
		log.WithFields(log.Fields{
			"name":    file.Name(),
			"runtime": endTime.Sub(startTime),
		}).Info("Deleted backup")
	}
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
	objectCh := svc.ListObjectsV2(bc.BucketName, "", isRecursive, doneCh)
	re := regexp.MustCompile(".+_etcd$")
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
		log.Info("Start to delete s3 backup file:", backupDeleteList[i])
		err := svc.RemoveObject(bc.BucketName, backupDeleteList[i])
		if err != nil {
			log.Error("Error detected during deletion: ", err)
		} else {
			log.Info("Success delete s3 backup file:", backupDeleteList[i])
		}
	}
}

func setS3Service(bc *backupConfig, useSSL bool) (*minio.Client, error) {
	// Initialize minio client object.
	log.Info("invoking set s3 service client")
	var err error
	var svc = &minio.Client{}

	for retries := 0; retries <= s3ServerRetries; retries++ {
		// if the s3 access key and secret is not set use iam role
		if len(bc.AccessKey) == 0 && len(bc.SecretKey) == 0 {
			log.Info("invoking set s3 service client use IAM role")
			iam := credentials.NewIAM("")
			svc, err = minio.NewWithCredentials("s3.amazonaws.com", iam, true, "")
		} else if len(bc.Region) != 0 {
			svc, err = minio.NewWithRegion(bc.Endpoint, bc.AccessKey, bc.SecretKey, useSSL, bc.Region)
		} else {
			svc, err = minio.New(bc.Endpoint, bc.AccessKey, bc.SecretKey, useSSL)
		}
		if err != nil {
			log.Infof("failed to init s3 client server: %v, retried %d times", err, retries)
			if retries >= s3ServerRetries {
				return nil, fmt.Errorf("failed to set s3 server: %v", err)
			}
			continue
		}
		break
	}

	found, err := svc.BucketExists(bc.BucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to check s3 bucket:%s, err:%v", bc.BucketName, err)
	}
	if !found {
		return nil, fmt.Errorf("bucket %s is not found", bc.BucketName)
	}
	return svc, nil
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
	bc := &backupConfig{
		Endpoint:   c.String("s3-endpoint"),
		AccessKey:  c.String("s3-accessKey"),
		SecretKey:  c.String("s3-secretKey"),
		BucketName: c.String("s3-bucketName"),
		Region:     c.String("s3-region"),
	}

	client, err := setS3Service(bc, true)
	if err != nil {
		log.WithFields(log.Fields{
			"s3-endpoint":   bc.Endpoint,
			"s3-bucketName": bc.BucketName,
			"s3-accessKey":  bc.AccessKey,
			"s3-region":     bc.Region,
		}).Errorf("failed to set s3 server: %s", err)
		return fmt.Errorf("faield to set s3 server: %+v", err)
	}

	fileName := c.String("name")
	if len(fileName) == 0 {
		return fmt.Errorf("empty file name")
	}

	log.Infof("invoking downloading backup files: %s", fileName)
	for retries := 0; retries <= s3ServerRetries; retries++ {
		err := client.FGetObject(bc.BucketName, fileName, backupBaseDir+"/"+fileName, minio.GetObjectOptions{})
		if err != nil {
			log.Infof("failed to download etcd snapshot file: %v, retried %d times", err, retries)
			if retries >= s3ServerRetries {
				return fmt.Errorf("failed to download etcd snapshot file: %v", err)
			}
			continue
		}
		log.Infof("Successfully download %s from s3 server", fileName)
		break
	}
	return nil
}
