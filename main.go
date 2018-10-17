package main

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	backupBaseDir = "/backup"
	backupRetries = 4
)

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
	app.Run(os.Args)
}

func RollingBackupCommand() cli.Command {
	return cli.Command{
		Name:   "rolling-backup",
		Usage:  "Perform rolling backups",
		Action: RollingBackupAction,
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "endpoints",
				Usage: "Etcd endpoints",
				Value: "127.0.0.1:2379",
			},
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
				Name:   "debug",
				Usage:  "Verbose logging information for debugging purposes",
				EnvVar: "RANCHER_DEBUG",
			},
			cli.BoolFlag{
				Name:  "once",
				Usage: "Take backup only once",
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
			cli.BoolTFlag{
				Name:  "s3",
				Usage: "Upload backup to AWS S3",
			},
			cli.StringFlag{
				Name:  "s3bucket",
				Usage: "AWS S3 bucket to upload to",
			},
			cli.StringFlag{
				Name:  "s3creds",
				Usage: "AWS S3 credentials (B64-encoded bytes)",
			},
			cli.StringFlag{
				Name:  "s3conf",
				Usage: "AWS S3 configuration (B64-encoded bytes)",
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
		return fmt.Errorf("Failed to find etcd cert or key paths")
	}
	log.WithFields(log.Fields{
		"creation":  creationPeriod,
		"retention": retentionPeriod,
	}).Info("Initializing Rolling Backups")

	s3bucket := ""
	if c.Bool("s3") {
		s3bucket = c.String("s3bucket")
		_ = os.Mkdir("/root/.aws", 0777)

		s3creds, err := base64.StdEncoding.DecodeString(c.String("s3creds"))
		if err != nil {
			return fmt.Errorf("Failed to decode S3 credentials")
		}
		err = ioutil.WriteFile("/root/.aws/credentials", s3creds, 0777)
		if err != nil {
			return fmt.Errorf("Failed to write AWS credentials file")
		}

		s3conf, err := base64.StdEncoding.DecodeString(c.String("s3conf"))
		if err != nil {
			return fmt.Errorf("Failed to decode S3 config")
		}
		err = ioutil.WriteFile("/root/.aws/config", s3conf, 0777)
		if err != nil {
			return fmt.Errorf("Failed to write AWS config file")
		}

	}

	if c.Bool("once") {
		backupName := c.String("name")
		if len(backupName) == 0 {
			backupName = fmt.Sprintf("%s_etcd", time.Now().Format(time.RFC3339))
		}
		err := CreateBackup(backupName, etcdCACert, etcdCert, etcdKey, etcdEndpoints)
		if err == nil && c.Bool("s3") {
			err = UploadBackupToS3(backupName, s3bucket)
		}
		return err
	}
	backupTicker := time.NewTicker(creationPeriod)
	for {
		select {
		case backupTime := <-backupTicker.C:
			backupName := fmt.Sprintf("%s_etcd", backupTime.Format(time.RFC3339))
			CreateBackup(backupName, etcdCACert, etcdCert, etcdKey, etcdEndpoints)
			if c.Bool("s3") {
				UploadBackupToS3(backupName, s3bucket)
			}
			DeleteBackups(backupTime, retentionPeriod)
		}
	}
}

func CreateBackup(backupName string, etcdCACert, etcdCert, etcdKey, endpoints string) error {
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
		break
	}
	return err
}

// UploadBackupToS3 uploads the snapshot to AWS S3, adding a <hostname> suffix to the name
func UploadBackupToS3(backupName, s3bucket string) error {
	// requires credentials in ~/.aws/credentials
	session := session.Must(session.NewSession())

	uploader := s3manager.NewUploader(session)
	backupDir := fmt.Sprintf("%s/%s", backupBaseDir, backupName)
	backup, err := os.Open(backupDir)
	if err != nil {
		return fmt.Errorf("Failed to open backup file %s, %v", backupDir, err)
	}

	// if we have a full S3 path, use the first part as bucket name and the rest for the key
	s3key := backupName
	split := strings.SplitN(s3bucket, "/", 2)
	if len(split) >= 2 {
		s3bucket = split[0]
		s3key = split[1]
		if !strings.HasSuffix(s3key, "/") {
			s3key = s3key + "/"
		}
		s3key = s3key + backupName
	}

	result, err := uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(s3bucket),
		Key:    aws.String(s3key),
		Body:   backup,
	})
	if err != nil {
		return fmt.Errorf("Failed to upload backup, %v", err)
	}
	log.WithFields(log.Fields{
		"backup": backupName,
		"bucket": result.Location,
	}).Info("Uploaded backup")

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
