package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FilesystemStorage implements Storage using the local filesystem
type FilesystemStorage struct {
	cacheDir string
}

// NewFilesystemStorage creates a new filesystem storage backend
func NewFilesystemStorage(cacheDir string) (*FilesystemStorage, error) {
	// Ensure cache directory exists
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &FilesystemStorage{
		cacheDir: cacheDir,
	}, nil
}

// GetIndex retrieves the cached index.json for a provider
func (fs *FilesystemStorage) GetIndex(ctx context.Context, hostname, namespace, providerType string) ([]byte, error) {
	path := fs.indexPath(hostname, namespace, providerType)
	return fs.readFile(ctx, path)
}

// PutIndex stores the index.json for a provider
func (fs *FilesystemStorage) PutIndex(ctx context.Context, hostname, namespace, providerType string, data []byte) error {
	path := fs.indexPath(hostname, namespace, providerType)
	return fs.writeFileAtomic(ctx, path, data)
}

// GetVersion retrieves the cached version.json for a specific provider version
func (fs *FilesystemStorage) GetVersion(ctx context.Context, hostname, namespace, providerType, version string) ([]byte, error) {
	path := fs.versionPath(hostname, namespace, providerType, version)
	return fs.readFile(ctx, path)
}

// PutVersion stores the version.json for a specific provider version
func (fs *FilesystemStorage) PutVersion(ctx context.Context, hostname, namespace, providerType, version string, data []byte) error {
	path := fs.versionPath(hostname, namespace, providerType, version)
	return fs.writeFileAtomic(ctx, path, data)
}

// GetArchive retrieves a cached provider archive
func (fs *FilesystemStorage) GetArchive(ctx context.Context, path string) (io.ReadCloser, error) {
	fullPath := fs.archivePath(path)
	file, err := os.Open(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("failed to open archive: %w", err)
	}
	return file, nil
}

// PutArchive stores a provider archive
func (fs *FilesystemStorage) PutArchive(ctx context.Context, path string, data io.Reader) error {
	fullPath := fs.archivePath(path)

	// Create directory if it doesn't exist
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create archive directory: %w", err)
	}

	// Write to temporary file first, then rename (atomic)
	tmpFile, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write archive: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temporary file: %w", err)
	}

	// Atomically move temp file to final location
	if err := os.Rename(tmpFile.Name(), fullPath); err != nil {
		return fmt.Errorf("failed to finalize archive: %w", err)
	}

	return nil
}

// ExistsArchive checks if an archive exists
func (fs *FilesystemStorage) ExistsArchive(ctx context.Context, path string) (bool, error) {
	fullPath := fs.archivePath(path)
	_, err := os.Stat(fullPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// GetVersionsResponse retrieves the cached full versions API response
func (fs *FilesystemStorage) GetVersionsResponse(ctx context.Context, hostname, namespace, providerType string) ([]byte, error) {
	path := fs.versionsResponsePath(hostname, namespace, providerType)
	return fs.readFile(ctx, path)
}

// PutVersionsResponse stores the full versions API response
func (fs *FilesystemStorage) PutVersionsResponse(ctx context.Context, hostname, namespace, providerType string, data []byte) error {
	path := fs.versionsResponsePath(hostname, namespace, providerType)
	return fs.writeFileAtomic(ctx, path, data)
}

// Helper methods

// indexPath constructs the filesystem path for an index.json file
// Matches terraform providers mirror structure: hostname/namespace/type/index.json
func (fs *FilesystemStorage) indexPath(hostname, namespace, providerType string) string {
	return filepath.Join(
		fs.cacheDir,
		hostname,
		namespace,
		providerType,
		"index.json",
	)
}

// versionPath constructs the filesystem path for a version.json file
// Matches terraform providers mirror structure: hostname/namespace/type/VERSION.json
func (fs *FilesystemStorage) versionPath(hostname, namespace, providerType, version string) string {
	return filepath.Join(
		fs.cacheDir,
		hostname,
		namespace,
		providerType,
		fmt.Sprintf("%s.json", version),
	)
}

// versionsResponsePath constructs the filesystem path for the full versions API response
// Stored in internal cache: .speculum-internal/hostname/namespace/type/versions.json
func (fs *FilesystemStorage) versionsResponsePath(hostname, namespace, providerType string) string {
	return filepath.Join(
		fs.cacheDir,
		".speculum-internal",
		hostname,
		namespace,
		providerType,
		"versions.json",
	)
}

// archivePath constructs the filesystem path for an archive file
// Archives are stored alongside metadata: hostname/namespace/type/archives/...
func (fs *FilesystemStorage) archivePath(path string) string {
	// Sanitize path to prevent directory traversal attacks
	sanitized := filepath.Clean(path)
	if strings.Contains(sanitized, "..") {
		sanitized = strings.ReplaceAll(sanitized, "..", "")
	}
	if strings.HasPrefix(sanitized, "/") {
		sanitized = sanitized[1:]
	}

	return filepath.Join(fs.cacheDir, sanitized)
}

// readFile reads a file from disk
func (fs *FilesystemStorage) readFile(ctx context.Context, path string) ([]byte, error) {
	// Ensure path is within cache directory
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	absCacheDir, err := filepath.Abs(fs.cacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve cache directory: %w", err)
	}

	if !strings.HasPrefix(absPath, absCacheDir) {
		return nil, errors.New("path is outside cache directory")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return data, nil
}

// writeFileAtomic writes a file atomically using a temporary file
func (fs *FilesystemStorage) writeFileAtomic(ctx context.Context, path string, data []byte) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write to temporary file first, then rename (atomic)
	tmpFile, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write data: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temporary file: %w", err)
	}

	// Atomically move temp file to final location
	if err := os.Rename(tmpFile.Name(), path); err != nil {
		return fmt.Errorf("failed to finalize write: %w", err)
	}

	return nil
}
