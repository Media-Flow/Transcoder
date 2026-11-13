package storage

import (
	"Transcoder/pkg/config"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	AWSConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type StorageRepository struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	region        string
	bucket        string
}

func NewStorageRepository(cfg *config.Config, region string) *StorageRepository {
	s3Configs, err := AWSConfig.LoadDefaultConfig(
		context.TODO(),
		AWSConfig.WithRegion(cfg.Storage.Region),
		AWSConfig.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     cfg.Storage.AccessKeyID,
				SecretAccessKey: cfg.Storage.SecretAccessKey,
			},
		}))

	if err != nil {
		log.Fatal(err)
	}

	s3Client := s3.NewFromConfig(s3Configs, func(o *s3.Options) {
		if cfg.Storage.Url != "" {
			o.BaseEndpoint = aws.String(cfg.Storage.Url)
			o.UsePathStyle = true
		}
	})

	return &StorageRepository{
		client:        s3Client,
		presignClient: s3.NewPresignClient(s3Client),
		region:        region,
		bucket:        cfg.Storage.BucketName,
	}
}

func (r *StorageRepository) Upload(ctx context.Context, key string, fileBytes io.Reader) error {
	if r.client == nil {
		return errors.New("s3 client not initialized")
	}
	_, err := r.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
		Body:   fileBytes,
	})
	if err != nil {
		return fmt.Errorf("failed to upload file %s: %w", key, err)
	}
	return nil
}

func (r *StorageRepository) GetPresignedUrl(ctx context.Context, fileName string) (string, error) {
	if r.presignClient == nil {
		return "", errors.New("presign client not initialized")
	}

	fileName = strings.TrimPrefix(fileName, r.bucket+"/")

	presignedUrl, err := r.presignClient.PresignGetObject(
		ctx,
		&s3.GetObjectInput{
			Bucket: aws.String(r.bucket),
			Key:    aws.String(fileName),
		},
	)
	if err != nil {
		return "", err
	}

	return presignedUrl.URL, nil
}
