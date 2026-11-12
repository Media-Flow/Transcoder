package storage

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type StorageService struct {
	repo *StorageRepository
}

func NewStorageService(repo *StorageRepository) *StorageService {
	return &StorageService{repo: repo}
}

func (s *StorageService) UploadMultipleFiles(ctx context.Context, mediaId string, rootDir string) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("storage repository is not initialized")
	}

	files, err := listFiles(rootDir)
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}

	for _, relPath := range files {
		if err := ctx.Err(); err != nil {
			return err
		}

		localPath := filepath.Join(rootDir, filepath.FromSlash(relPath))

		file, err := os.Open(localPath)
		if err != nil {
			return fmt.Errorf("failed to open file %q: %w", localPath, err)
		}

		key := HLSKey(mediaId, relPath)
		uploadErr := s.repo.Upload(ctx, key, file)

		closeErr := file.Close()

		if uploadErr != nil {
			return uploadErr
		}

		if closeErr != nil {
			return fmt.Errorf("failed to close file %q: %w", localPath, closeErr)
		}
	}

	return nil
}

func listFiles(rootDir string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			relPath, err := filepath.Rel(rootDir, path)
			if err != nil {
				return err
			}

			files = append(files, filepath.ToSlash(relPath))
		}

		return nil
	})

	return files, err
}

func (s *StorageService) GetPresignedUrl(ctx context.Context, mediaKey string) (string, error) {
	if s == nil || s.repo == nil {
		return "", fmt.Errorf("storage repository is not initialized")
	}

	return s.repo.GetPresignedUrl(ctx, mediaKey)
}
