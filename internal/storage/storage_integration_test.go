package storage

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"Transcoder/pkg/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	minioImage           = "quay.io/minio/minio:latest"
	minioAccessKeyID     = "minioadmin"
	minioSecretAccessKey = "minioadmin"
	minioRegion          = "us-east-1"
	testBucketPrefix     = "transcoder-storage-test"
)

var testEndpoint string

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	if testing.Short() {
		fmt.Println("skipping storage integration tests in short mode")
		return 0
	}

	ctx := context.Background()
	containerName := fmt.Sprintf("transcoder-storage-test-%d", os.Getpid())

	endpoint, cleanup, err := startMinIO(ctx, containerName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage integration setup: %v\n", err)
		return 1
	}
	defer cleanup()

	testEndpoint = endpoint

	return m.Run()
}

func startMinIO(ctx context.Context, name string) (string, func(), error) {
	_ = exec.Command("docker", "rm", "-f", name).Run()

	run := exec.CommandContext(
		ctx,
		"docker", "run", "--rm", "-d",
		"--name", name,
		"-e", "MINIO_ROOT_USER="+minioAccessKeyID,
		"-e", "MINIO_ROOT_PASSWORD="+minioSecretAccessKey,
		"-p", "127.0.0.1::9000",
		minioImage,
		"server", "/data",
	)
	out, err := run.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("docker run: %w: %s", err, strings.TrimSpace(string(out)))
	}

	containerID := strings.TrimSpace(string(out))
	cleanup := func() {
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
	}

	portOut, err := exec.CommandContext(ctx, "docker", "port", containerID, "9000/tcp").Output()
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("docker port: %w", err)
	}

	addr := strings.TrimSpace(strings.Split(string(portOut), "\n")[0])
	endpoint := "http://" + addr

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	for {
		req, reqErr := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			endpoint+"/minio/health/ready",
			nil,
		)
		if reqErr == nil {
			resp, doErr := client.Do(req)
			if doErr == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return endpoint, cleanup, nil
				}
			}
		}

		if time.Now().After(deadline) {
			cleanup()
			return "", nil, errors.New("timed out waiting for MinIO")
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func newTestStorage(t *testing.T) (*StorageRepository, *StorageService) {
	t.Helper()

	bucket := fmt.Sprintf(
		"%s-%d-%d",
		testBucketPrefix,
		os.Getpid(),
		time.Now().UnixNano(),
	)
	cfg := &config.Config{
		Storage: config.StorageConfig{
			BucketName:      bucket,
			SecretAccessKey: minioSecretAccessKey,
			AccessKeyID:     minioAccessKeyID,
			Region:          minioRegion,
			Url:             testEndpoint,
		},
	}

	repo := NewStorageRepository(cfg, cfg.Storage.Region)
	service := &StorageService{repo: repo}

	ctx := context.Background()
	if _, err := repo.client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	t.Cleanup(func() {
		cleanupBucket(context.Background(), repo, bucket)
	})

	return repo, service
}

func cleanupBucket(ctx context.Context, repo *StorageRepository, bucket string) {
	paginator := s3.NewListObjectsV2Paginator(repo.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return
		}

		objects := make([]types.ObjectIdentifier, 0, len(page.Contents))
		for _, object := range page.Contents {
			objects = append(objects, types.ObjectIdentifier{Key: object.Key})
		}

		if len(objects) == 0 {
			continue
		}

		if _, err := repo.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: objects},
		}); err != nil {
			return
		}
	}

	_, _ = repo.client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucket),
	})
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create directory for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %q: %v", path, err)
	}
}

func listObjectKeys(
	t *testing.T,
	ctx context.Context,
	repo *StorageRepository,
	bucket string,
) []string {
	t.Helper()

	var keys []string
	paginator := s3.NewListObjectsV2Paginator(repo.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			t.Fatalf("list objects: %v", err)
		}

		for _, object := range page.Contents {
			keys = append(keys, aws.ToString(object.Key))
		}
	}

	sort.Strings(keys)
	return keys
}

func presignedContent(
	t *testing.T,
	ctx context.Context,
	repo *StorageRepository,
	key string,
) string {
	t.Helper()

	url, err := repo.GetPresignedUrl(ctx, key)
	if err != nil {
		t.Fatalf("presign %q: %v", key, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("create presigned request %q: %v", key, err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get presigned %q: %v", key, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read presigned %q: %v", key, err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get presigned %q: status %d: %s", key, resp.StatusCode, body)
	}

	return string(body)
}

func TestStorageRepositoryUploadSingleFile(t *testing.T) {
	repo, _ := newTestStorage(t)
	ctx := context.Background()

	content := "single file content\n"
	localPath := filepath.Join(t.TempDir(), "single.txt")
	writeTestFile(t, localPath, content)

	file, err := os.Open(localPath)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}

	uploadErr := repo.Upload(ctx, "single.txt", file)
	closeErr := file.Close()

	if uploadErr != nil {
		t.Fatalf("upload: %v", uploadErr)
	}
	if closeErr != nil {
		t.Fatalf("close file: %v", closeErr)
	}

	if got := presignedContent(t, ctx, repo, "single.txt"); got != content {
		t.Fatalf("content = %q, want %q", got, content)
	}
}

func TestStorageServiceUploadMultipleFilesPreservesHierarchy(t *testing.T) {
	repo, service := newTestStorage(t)
	ctx := context.Background()

	mediaId := "Video-1"
	rootDir := filepath.Join(t.TempDir(), "app")
	files := map[string]string{
		"file1.txt":       "file one content\n",
		"dir-1/file2.txt": "file two content\n",
		"dir-1/file3.txt": "file three content\n",
	}

	for relPath, content := range files {
		writeTestFile(t, filepath.Join(rootDir, filepath.FromSlash(relPath)), content)
	}

	if err := service.UploadMultipleFiles(ctx, mediaId, rootDir); err != nil {
		t.Fatalf("upload multiple files: %v", err)
	}

	wantKeys := []string{
		"Video-1/hls/dir-1/file2.txt",
		"Video-1/hls/dir-1/file3.txt",
		"Video-1/hls/file1.txt",
	}
	gotKeys := listObjectKeys(t, ctx, repo, repo.bucket)
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("object keys = %v, want %v", gotKeys, wantKeys)
	}

	for relPath, content := range files {
		key := HLSKey(mediaId, relPath)
		if got := presignedContent(t, ctx, repo, key); got != content {
			t.Fatalf("content for %q = %q, want %q", key, got, content)
		}
	}
}
