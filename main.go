package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	backupBaseDir         = "/backup"
	defaultBackupRetries  = 4
	clusterStateExtension = "rkestate"
	compressedExtension   = "zip"
	contentType           = "application/zip"
	k8sBaseDir            = "/etc/kubernetes"
	defaultS3Retries      = 3
	serverPort            = "2379"
	s3Endpoint            = "s3.amazonaws.com"
	tmpStateFilePath      = "/tmp/cluster.rkestate"
	failureInterval       = 15 * time.Second
)

var (
	backupRetries uint = defaultBackupRetries
	s3Retries     uint = defaultS3Retries
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
		Usage:  "Backup etcd snapshot to your s3 server, set true or false",
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

var deleteFlags = []cli.Flag{
	cli.StringFlag{
		Name:  "name",
		Usage: "snapshot name to delete",
	},
	cli.BoolFlag{
		Name:  "s3-backup",
		Usage: "delete snapshot from s3",
	},
	cli.BoolFlag{
		Name:  "cleanup",
		Usage: "delete uncompressed files only",
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
		BackupCommand(),
	}
	err = app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func BackupCommand() cli.Command {

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
				Name:  "save",
				Usage: "Take snapshot on all etcd hosts and backup to s3 compatible storage",
				Flags: append(snapshotFlags, cli.UintFlag{
					Name:        "backup-retries",
					Usage:       "Number of times to attempt the backup",
					Destination: &backupRetries,
				}, cli.UintFlag{
					Name:        "s3-retries",
					Usage:       "Number of times to attempt the upload to s3",
					Destination: &s3Retries,
				}),
				Action: SaveBackupAction,
			},
			{
				Name:   "delete",
				Usage:  "Delete snapshot from etcd hosts or s3 compatible storage",
				Flags:  deleteFlags,
				Action: DeleteBackupAction,
			},
			{
				Name:   "download",
				Usage:  "Download specified snapshot from s3 compatible storage or another local endpoint",
				Flags:  commonFlags,
				Action: DownloadBackupAction,
			},
			{
				Name:   "extractstatefile",
				Usage:  "Extract statefile for specified snapshot (if it is included in the archive)",
				Flags:  snapshotFlags,
				Action: ExtractStateFileAction,
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
		log.Debug("Log level set to debug")
	} else {
		log.SetLevel(log.InfoLevel)
	}
}

func SaveBackupAction(c *cli.Context) error {
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

	if c.Bool("once") {
		backupName := c.String("name")

		log.WithFields(log.Fields{
			"name": backupName,
		}).Info("Initializing Onetime Backup")

		compressedFilePath, err := CreateBackup(backupName, etcdCACert, etcdCert, etcdKey, etcdEndpoints, backupRetries)
		if err != nil {
			return err
		}
		if bc.Backup {
			err = CreateS3Backup(backupName, compressedFilePath, bc)
			if err != nil {
				return err
			}
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
	log.WithFields(log.Fields{
		"creation":  creationPeriod,
		"retention": retentionPeriod,
	}).Info("Initializing Rolling Backups")

	backupTicker := time.NewTicker(creationPeriod)
	for {
		select {
		case backupTime := <-backupTicker.C:
			backupName := fmt.Sprintf("%s_etcd", backupTime.Format(time.RFC3339))
			err := retrieveAndWriteStatefile(backupName)
			if err != nil {
				// An error on statefile retrieval is not a reason to bail out
				// Having a snapshot without a statefile is more valuable than not having a snapshot at all
				log.WithFields(log.Fields{
					"name":  backupName,
					"error": err,
				}).Warn("Error while trying to retrieve cluster state from cluster")
			}
			compressedFilePath, err := CreateBackup(backupName, etcdCACert, etcdCert, etcdKey, etcdEndpoints, backupRetries)
			if err != nil {
				continue
			}
			DeleteBackups(backupTime, retentionPeriod)
			if !bc.Backup {
				continue
			}
			err = CreateS3Backup(backupName, compressedFilePath, bc)
			if err != nil {
				continue
			}
			DeleteS3Backups(backupTime, retentionPeriod, bc)
		}
	}
}

func minioClientFromConfig(bc *backupConfig) (*minio.Client, error) {
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
		return nil, fmt.Errorf("failed to set s3 server: %+v", err)
	}
	return client, nil
}

func CreateBackup(backupName, etcdCACert, etcdCert, etcdKey, endpoints string, backupRetries uint) (compressedFilePath string, err error) {
	backupFile := fmt.Sprintf("%s/%s", backupBaseDir, backupName)
	stateFile := fmt.Sprintf("%s/%s.%s", k8sBaseDir, backupName, clusterStateExtension)
	var data []byte
	for retries := uint(0); retries <= backupRetries; retries++ {
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
		data, err = cmd.CombinedOutput()

		if strings.Contains(string(data), "unhealthy") {
			log.WithFields(log.Fields{
				"error": err,
				"data":  string(data),
			}).Warn("Checking member health failed from etcd member")
			err = fmt.Errorf("%s: %v", err, string(data))
			continue
		}

		cmd = exec.Command("etcdctl",
			fmt.Sprintf("--endpoints=%s", endpoints),
			"--cacert="+etcdCACert,
			"--cert="+etcdCert,
			"--key="+etcdKey,
			"snapshot", "save", backupFile)

		startTime := time.Now()
		data, err = cmd.CombinedOutput()
		endTime := time.Now()

		if err != nil {
			log.WithFields(log.Fields{
				"attempt": retries + 1,
				"error":   err,
				"data":    string(data),
			}).Warn("Backup failed")
			err = fmt.Errorf("%s: %v", err, string(data))
			continue
		}
		// Determine how many files need to be in the compressed file
		// 1. the compressed file
		toCompressFiles := []string{backupFile}
		// 2. the state file if present
		if _, err = os.Stat(stateFile); err == nil {
			toCompressFiles = append(toCompressFiles, stateFile)
		}
		// Create compressed file
		compressedFilePath, err = compressFiles(backupFile, toCompressFiles)
		if err != nil {
			log.WithFields(log.Fields{
				"attempt": retries + 1,
				"error":   err,
				"data":    string(data),
			}).Warn("Compressing backup failed")
			continue
		}
		// Remove the original file after successfully compressing it
		err = os.Remove(backupFile)
		if err != nil {
			log.WithFields(log.Fields{
				"attempt": retries + 1,
				"error":   err,
				"data":    string(data),
			}).Warn("Removing uncompressed snapshot file failed")
			continue

		}
		// Remove the state file after successfully compressing it
		if _, err = os.Stat(stateFile); err == nil {
			err = os.Remove(stateFile)
			if err != nil {
				log.WithFields(log.Fields{
					"attempt": retries + 1,
					"error":   err,
					"data":    string(data),
				}).Warn("Removing statefile failed")
			}
		}

		log.WithFields(log.Fields{
			"name":    backupName,
			"runtime": endTime.Sub(startTime),
		}).Info("Created local backup")

		if err = os.Chmod(compressedFilePath, 0600); err != nil {
			log.WithFields(log.Fields{
				"attempt": retries + 1,
				"error":   err,
				"data":    string(data),
			}).Warn("changing permission of the compressed snapshot failed")
			continue
		}
		break
	}
	return
}

func CreateS3Backup(backupName, compressedFilePath string, bc *backupConfig) error {
	// If the minio client doesn't work now, it won't after retrying
	client, err := minioClientFromConfig(bc)
	if err != nil {
		return err
	}
	compressedFile := filepath.Base(compressedFilePath)
	// If folder is specified, prefix the file with the folder
	if len(bc.Folder) != 0 {
		compressedFile = fmt.Sprintf("%s/%s", bc.Folder, compressedFile)
	}
	// check if it exists already in the bucket, and if versioning is disabled on the bucket. If an error is detected,
	// assume we aren't privy to that information and do multiple uploads anyway.
	info, _ := client.StatObject(context.TODO(), bc.BucketName, compressedFile, minio.StatObjectOptions{})
	if info.Size != 0 {
		versioning, _ := client.GetBucketVersioning(context.TODO(), bc.BucketName)
		if !versioning.Enabled() {
			log.WithFields(log.Fields{
				"name": backupName,
			}).Info("Skipping upload to s3 because snapshot already exists and versioning is not enabled for the bucket")
			return nil
		}
	}

	err = uploadBackupFile(client, bc.BucketName, compressedFile, compressedFilePath, s3Retries)
	if err != nil {
		return err
	}
	return nil
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
			_ = deleteBackup(file.Name())
		}
	}
}

func deleteBackup(fileName string) error {
	toDelete := fmt.Sprintf("%s/%s", backupBaseDir, path.Base(fileName))

	cmd := exec.Command("rm", "-f", toDelete)

	startTime := time.Now()
	err2 := cmd.Run()
	endTime := time.Now()

	if err2 != nil {
		log.WithFields(log.Fields{
			"name":  fileName,
			"error": err2,
		}).Warn("Delete local backup failed")
		return err2
	}
	log.WithFields(log.Fields{
		"name":    fileName,
		"runtime": endTime.Sub(startTime),
	}).Info("Deleted local backup")
	return nil
}

func DeleteS3Backups(backupTime time.Time, retentionPeriod time.Duration, bc *backupConfig) {
	log.WithFields(log.Fields{
		"retention": retentionPeriod,
	}).Info("Invoking delete s3 backup files")
	var backupDeleteList []string
	client, err := minioClientFromConfig(bc)
	if err != nil {
		// An error on setting minio client is not a reason to bail out
		// Having a snapshot without an upload to s3 is more valuable than not having a snapshot at all
		log.WithFields(log.Fields{
			"error": err,
		}).Warn("Error while trying to configure minio client")
		return
	}

	cutoffTime := backupTime.Add(retentionPeriod * -1)

	isRecursive := false
	prefix := ""
	if len(bc.Folder) != 0 {
		prefix = bc.Folder
		// Recurse will show us the files in the folder
		isRecursive = true
	}
	objectCh := client.ListObjects(context.TODO(), bc.BucketName, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: isRecursive,
	})
	re := regexp.MustCompile(fmt.Sprintf(".+_etcd(|.%s)$", compressedExtension))
	for object := range objectCh {
		if object.Err != nil {
			log.Error("error to fetch s3 file:", object.Err)
			return
		}
		// only parse backup file names that matches *_etcd format
		if re.MatchString(object.Key) {
			filename := object.Key

			if len(bc.Folder) != 0 {
				// example object.Key with folder: folder/timestamp_etcd.zip
				// folder and separator needs to be stripped so time can be parsed below
				log.Debugf("Stripping [%s] from [%s]", fmt.Sprintf("%s/", prefix), filename)
				filename = strings.TrimPrefix(filename, fmt.Sprintf("%s/", prefix))
			}
			log.Debugf("object.Key: [%s], filename: [%s]", object.Key, filename)

			backupTime, err := time.Parse(time.RFC3339, strings.Split(filename, "_")[0])
			if err != nil {
				log.WithFields(log.Fields{
					"name":      filename,
					"objectKey": object.Key,
					"error":     err,
				}).Warn("Couldn't parse s3 backup")

			} else if backupTime.Before(cutoffTime) {
				// We use object.Key here as we need the full path when a folder is used
				log.Debugf("Adding [%s] to files to delete, backupTime: [%q], cutoffTime: [%q]", object.Key, backupTime, cutoffTime)
				backupDeleteList = append(backupDeleteList, object.Key)
			}
		}
	}
	log.Debugf("Found %d files to delete", len(backupDeleteList))

	for i := range backupDeleteList {
		log.Infof("Start to delete s3 backup file [%s]", backupDeleteList[i])
		err := client.RemoveObject(context.TODO(), bc.BucketName, backupDeleteList[i], minio.RemoveObjectOptions{})
		if err != nil {
			log.Errorf("Error detected during deletion: %v", err)
		} else {
			log.Infof("Success delete s3 backup file [%s]", backupDeleteList[i])
		}
	}
}

func DeleteBackupAction(c *cli.Context) error {
	name := c.String("name")
	if name == "" {
		return fmt.Errorf("snapshot name is required")
	}
	compressedPath := fmt.Sprintf("/backup/%s.%s", name, compressedExtension)
	uncompressedPath := fmt.Sprintf("/backup/%s", name)

	// Since we have to support compressed and uncompressed versions of snapshots.
	// We can't remove the uncompressed snapshot during cleanup unless we are
	// sure the compressed is there, hence the complex check.
	if c.Bool("cleanup") {
		if _, err := os.Stat(compressedPath); err == nil {
			// for cleanup, we only want to delete the uncompressed snapshot.
			// we don't need to go to s3
			return deleteBackup(uncompressedPath)
		}
	} else {
		for _, p := range []string{compressedPath, uncompressedPath} {
			if err := deleteBackup(p); err != nil {
				return err
			}
		}
	}

	if !c.Bool("s3-backup") {
		return nil
	}

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
	folder := c.String("s3-folder")
	if len(folder) != 0 {
		name = fmt.Sprintf("%s/%s", folder, name)
	}

	doneCh := make(chan struct{})
	defer close(doneCh)
	// list objects with prefix=name, this will include uncompressed and compressed backup objects
	objectCh := client.ListObjects(context.TODO(), bc.BucketName, minio.ListObjectsOptions{
		Prefix:    name,
		Recursive: false,
	})
	var removed []string
	for object := range objectCh {
		if object.Err != nil {
			log.Errorf("failed to list objects in backup buckets [%s]: %v", bc.BucketName, object.Err)
			return object.Err
		}
		log.Infof("deleting object with key: %s that matches prefix: %s", object.Key, name)
		err = client.RemoveObject(context.TODO(), bc.BucketName, object.Key, minio.RemoveObjectOptions{})
		if err != nil {
			return err
		}
		removed = append(removed, object.Key)
	}

	log.Infof("removed backups: %s from object store", strings.Join(removed, ", "))

	return nil
}

func setS3Service(bc *backupConfig, useSSL bool) (*minio.Client, error) {
	// Initialize minio client object.
	log.WithFields(log.Fields{
		"s3-endpoint":    bc.Endpoint,
		"s3-bucketName":  bc.BucketName,
		"s3-accessKey":   bc.AccessKey,
		"s3-region":      bc.Region,
		"s3-endpoint-ca": bc.EndpointCA,
		"s3-folder":      bc.Folder,
	}).Info("invoking set s3 service client")

	var err error
	var client = &minio.Client{}
	var cred = &credentials.Credentials{}
	var tr = http.DefaultTransport
	if bc.EndpointCA != "" {
		tr, err = setTransportCA(tr, bc.EndpointCA)
		if err != nil {
			return nil, err
		}
	}
	bucketLookup := getBucketLookupType(bc.Endpoint)
	for retries := 0; retries <= defaultS3Retries; retries++ {
		// if the s3 access key and secret is not set use iam role
		if len(bc.AccessKey) == 0 && len(bc.SecretKey) == 0 {
			log.Info("invoking set s3 service client use IAM role")
			cred = credentials.NewIAM("")
			if bc.Endpoint == "" {
				bc.Endpoint = s3Endpoint
			}
		} else {
			// Base64 decoding S3 accessKey and secretKey before create static credentials
			// To be backward compatible, just updating base64 encoded values
			accessKey := bc.AccessKey
			secretKey := bc.SecretKey
			if len(accessKey) > 0 {
				v, err := base64.StdEncoding.DecodeString(accessKey)
				if err == nil {
					accessKey = string(v)
				}
			}
			if len(secretKey) > 0 {
				v, err := base64.StdEncoding.DecodeString(secretKey)
				if err == nil {
					secretKey = string(v)
				}
			}
			cred = credentials.NewStatic(accessKey, secretKey, "", credentials.SignatureDefault)
		}
		client, err = minio.New(bc.Endpoint, &minio.Options{
			Creds:        cred,
			Secure:       useSSL,
			Region:       bc.Region,
			BucketLookup: bucketLookup,
			Transport:    tr,
		})
		if err != nil {
			log.Infof("failed to init s3 client server: %v, retried %d times", err, retries)
			if retries >= defaultS3Retries {
				return nil, fmt.Errorf("failed to set s3 server: %v", err)
			}
			continue
		}

		break
	}

	found, err := client.BucketExists(context.TODO(), bc.BucketName)
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

func uploadBackupFile(svc *minio.Client, bucketName, fileName, filePath string, s3Retries uint) error {
	var info minio.UploadInfo
	var err error
	// Upload the zip file with FPutObject
	log.Infof("invoking uploading backup file [%s] to s3", fileName)
	for i := uint(0); i <= s3Retries; i++ {
		info, err = svc.FPutObject(context.TODO(), bucketName, fileName, filePath, minio.PutObjectOptions{ContentType: contentType})
		if err == nil {
			log.Infof("Successfully uploaded [%s] of size [%d]", fileName, info.Size)
			return nil
		}
		log.Infof("failed to upload etcd snapshot file: %v, retried %d times", err, i)
	}
	return fmt.Errorf("failed to upload etcd snapshot file: %v", err)
}

func DownloadBackupAction(c *cli.Context) error {
	log.Info("Initializing Download Backups")
	SetLoggingLevel(c.Bool("debug"))
	if c.Bool("s3-backup") {
		return DownloadS3Backup(c)
	}
	return DownloadLocalBackup(c)
}

func ExtractStateFileAction(c *cli.Context) error {
	SetLoggingLevel(c.Bool("debug"))
	name := path.Base(c.String("name"))
	log.Infof("Trying to get statefile from backup [%s]", name)
	if c.Bool("s3-backup") {
		err := DownloadS3Backup(c)
		if err != nil {
			return err
		}
	}
	// Destination filename for statefile
	stateFilePath := fmt.Sprintf("%s/%s.%s", k8sBaseDir, name, clusterStateExtension)
	// Location of the compressed snapshot file
	compressedFilePath := fmt.Sprintf("/backup/%s.%s", name, compressedExtension)
	// Check if compressed snapshot file exists
	if _, err := os.Stat(compressedFilePath); err != nil {
		return err
	}
	// Extract statefile content in archive
	err := decompressFile(compressedFilePath, stateFilePath, tmpStateFilePath)
	if err != nil {
		return fmt.Errorf("Unable to extract file [%s] from file [%s] to destination [%s]: %v", stateFilePath, compressedFilePath, tmpStateFilePath, err)
	}
	log.Infof("Successfully extracted file [%s] from file [%s] to destination [%s]", stateFilePath, compressedFilePath, tmpStateFilePath)

	return nil
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
		compressedFilePath := fmt.Sprintf("%s/%s", backupBaseDir, filename)
		fileLocation := fmt.Sprintf("%s/%s", backupBaseDir, decompressedName(filename))
		err := decompressFile(compressedFilePath, fileLocation, fileLocation)
		if err != nil {
			return fmt.Errorf("Unable to decompress [%s] to [%s]: %v", compressedFilePath, fileLocation, err)
		}

		log.Infof("Decompressed [%s] to [%s]", compressedFilePath, fileLocation)
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
	snapshotURL := fmt.Sprintf("https://%s:%s/%s", endpoint, serverPort, snapshot)
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
			if err = deleteBackup(file.Name()); err != nil {
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
	compressedFilePath := fmt.Sprintf("%s/%s.%s", backupBaseDir, snapshot, compressedExtension)
	fileLocation := fmt.Sprintf("%s/%s", backupBaseDir, snapshot)
	if _, err := os.Stat(compressedFilePath); err == nil {
		err := decompressFile(compressedFilePath, fileLocation, fileLocation)
		if err != nil {
			return err
		}
		log.Infof("Extracted from %s", compressedFilePath)
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
		Addr:      fmt.Sprintf("0.0.0.0:%s", serverPort),
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
	// typeFlag = "m": manual
	//
	// providerFlag = "l" local
	// providerFlag = "s" s3
	re := regexp.MustCompile("^c-[a-z0-9].*?-r.-")
	return re.MatchString(name)
}

func downloadFromS3WithPrefix(client *minio.Client, prefix, bucket string) (string, error) {
	var filename string

	objectCh := client.ListObjects(context.TODO(), bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: false,
	})
	for object := range objectCh {
		if object.Err != nil {
			log.Errorf("failed to list objects in backup buckets [%s]: %v", bucket, object.Err)
			return "", object.Err
		}
		decompressedFilename := decompressedName(object.Key)
		log.Debugf("found key: [%s], decompressedFilename: [%s]", object.Key, decompressedFilename)
		if prefix == decompressedFilename {
			filename = object.Key
			break
		}
		decodedDecompressedFilename, err := url.QueryUnescape(decompressedFilename)
		if err != nil {
			log.Errorf("Unable to decode filename [%s]: %v", decompressedFilename, err)
			continue
		}
		if prefix == decodedDecompressedFilename {
			decodedObjectKey, err := url.QueryUnescape(object.Key)
			if err != nil {
				log.Errorf("Unable to decode object.Key [%s]: %v", object.Key, err)
				continue
			}
			filename = decodedObjectKey
			break
		}
	}
	if len(filename) == 0 {
		return "", fmt.Errorf("failed to download s3 backup: no backups found")
	}
	// if folder is included, strip it so it doesn't end up in a folder on the host itself
	targetFilename := path.Base(filename)
	targetFileLocation := fmt.Sprintf("%s/%s", backupBaseDir, targetFilename)
	var object *minio.Object
	var err error

	for retries := 0; retries <= defaultS3Retries; retries++ {
		object, err = client.GetObject(context.TODO(), bucket, filename, minio.GetObjectOptions{})
		if err != nil {
			log.Infof("Failed to download etcd snapshot file [%s]: %v, retried %d times", filename, err, retries)
			if retries >= defaultS3Retries {
				return "", fmt.Errorf("Unable to download backup file for [%s]: %v", filename, err)
			}
		}
		log.Infof("Successfully downloaded [%s]", filename)
	}

	localFile, err := os.Create(targetFileLocation)
	if err != nil {
		return "", fmt.Errorf("Failed to create local file [%s]: %v", targetFileLocation, err)
	}
	defer localFile.Close()

	if _, err = io.Copy(localFile, object); err != nil {
		return "", fmt.Errorf("Failed to copy retrieved object to local file [%s]: %v", targetFileLocation, err)
	}
	if err := os.Chmod(targetFileLocation, 0600); err != nil {
		return "", fmt.Errorf("changing permission of the locally downloaded snapshot failed")
	}

	return targetFilename, nil
}

func compressFiles(destinationFile string, fileNames []string) (string, error) {
	// Create destination file
	compressedFile := fmt.Sprintf("%s.%s", destinationFile, compressedExtension)
	zipFile, err := os.Create(compressedFile)
	if err != nil {
		return "", err
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	for _, file := range fileNames {
		if err = AddFileToZip(zipWriter, file); err != nil {
			return "", err
		}
	}
	return compressedFile, nil
}

// decompressFile: Thanks to https://golangcode.com/unzip-files-in-go/
func decompressFile(src string, filePath string, dest string) error {

	var fileFound bool

	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == filePath {
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
			fileFound = true
			break
		}
	}

	if fileFound {
		if err := os.Chmod(dest, 0600); err != nil {
			log.WithFields(log.Fields{
				"error": err,
			}).Warn("changing permission of the decompressed snapshot failed")
		}
		return nil
	}
	return fmt.Errorf("File [%s] not found in file [%s]", filePath, src)
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

func AddFileToZip(zipWriter *zip.Writer, filename string) error {
	fileToZip, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer fileToZip.Close()

	// Get the file information
	info, err := fileToZip.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}

	// Using FileInfoHeader() above only uses the basename of the file. If we want
	// to preserve the folder structure we can overwrite this with the full path.
	header.Name = filename

	// Change to deflate to gain better compression
	// see http://golang.org/pkg/archive/zip/#pkg-constants
	header.Method = zip.Deflate
	header.Modified = time.Unix(0, 0)

	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(writer, fileToZip)
	return err
}

func retrieveAndWriteStatefile(backupName string) error {
	log.WithFields(log.Fields{
		"name": backupName,
	}).Debug("retrieveAndWriteStatefile called")

	var out bytes.Buffer
	var err error
	for retries := 0; retries <= defaultBackupRetries; retries++ {
		log.WithFields(log.Fields{
			"attempt": retries + 1,
			"name":    backupName,
		}).Info("Trying to retrieve configmap full-cluster-state using kubectl")

		if retries > 0 {
			time.Sleep(failureInterval)
		}

		// Try to retrieve cluster state to include in snapshot
		cmd := exec.Command("/usr/local/bin/kubectl", "--request-timeout=30s", "--kubeconfig", "/etc/kubernetes/ssl/kubecfg-kube-node.yaml", "-n", "kube-system", "get", "configmap", "full-cluster-state", "-o", "json")
		var stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		err = cmd.Run()
		if err != nil {
			log.WithFields(log.Fields{
				"attempt": retries + 1,
				"name":    backupName,
				"err":     fmt.Sprintf("%s: %s", err, stderr.String()),
			}).Warn("Failed to retrieve configmap full-cluster-state using kubectl")
			if retries >= defaultBackupRetries {
				return fmt.Errorf("Failed to retrieve configmap full-cluster-state using kubectl: %v", fmt.Sprintf("%s: %s", err, stderr.String()))
			}
			continue
		}
		break
	}
	var m map[string]interface{}
	err = json.Unmarshal(out.Bytes(), &m)
	if err != nil {
		return fmt.Errorf("Failed to unmarshal cluster state from configmap full-cluster-state: %v", err)
	}

	var jsondata map[string]interface{}
	var fullClusterState string
	if _, ok := m["data"]; ok {
		jsondata = m["data"].(map[string]interface{})
	}
	if str, ok := jsondata["full-cluster-state"].(string); ok {
		fullClusterState = str
	}

	var prettyFullClusterState bytes.Buffer
	err = json.Indent(&prettyFullClusterState, []byte(fullClusterState), "", "  ")
	if err != nil {
		return fmt.Errorf("Failed to indent JSON for state file: %v", err)
	}
	stateFilePath := fmt.Sprintf("/etc/kubernetes/%s.rkestate", backupName)
	f, err := os.Create(stateFilePath)
	if err != nil {
		return fmt.Errorf("Failed to create state file [%s]: %v", stateFilePath, err)
	}
	defer f.Close()
	_, err = f.Write(prettyFullClusterState.Bytes())
	if err != nil {
		return fmt.Errorf("Failed to write state file [%s]: %v", stateFilePath, err)
	}
	log.WithFields(log.Fields{
		"filepath": stateFilePath,
	}).Info("Successfully written state file content to file")

	return nil
}
