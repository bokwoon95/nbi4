package nbi4

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/bokwoon95/nbi4/stacktrace"
)

// RangeNotSatisfiableError represents the HTTP 416 Range Not Satisfiable
// error.
type RangeNotSatisfiableError struct {
	Key   string
	Start int64
	End   int64
	Size  int64
}

// Error implements the error interface.
func (e *RangeNotSatisfiableError) Error() string {
	if e.Start < 0 {
		return fmt.Sprintf("range not satisfiable: start cannot be less than 0: key=%s, start=%d", e.Key, e.Start)
	}
	if e.End < e.Start {
		return fmt.Sprintf("range not satisfiable: end cannot be less than start: key=%s, start=%d, end=%d", e.Key, e.Start, e.End)
	}
	if e.Size >= 0 {
		return fmt.Sprintf("range not satisfiable: start exceeds object size: key=%s, start=%d, size=%d", e.Key, e.Start, e.Size)
	}
	return fmt.Sprintf("range not satisfiable: start exceeds object size: key=%s, start=%d, size=unknown", e.Key, e.Start)
}

// Object keys should follow this format: 2001/2001-02-03/0123456789 2001-02-03 040506.123 +0800.jpeg

// ObjectStorage represents an object storage provider.
type ObjectStorage interface {
	// GetRange gets an object from the bucket, optionally for a given range of
	// bytes. If no ranging is desired, wantRange should be the zero array
	// [2]int64{}.
	//
	// If wantRange is not the zero array:
	// - wantRange[0] is the desired starting byte index.
	// - wantRange[1] is the desired ending byte index (inclusive). It can be
	// left as 0 to indicate ranging to the end of the object.
	//
	// If gotRange is not the zero array:
	// - gotRange[0] is the actual starting byte index of the readCloser.
	// - gotRange[1] is the actual ending byte index of the readCloser.
	// - gotRange[2] is the size (in bytes) of the readCloser. It is 0 if
	// unknown.
	GetRange(ctx context.Context, key string, wantRange [2]int64) (readCloser io.ReadCloser, gotRange [3]int64, err error)

	// Puts an object into a bucket. If key already exists, it should be
	// replaced.
	Put(ctx context.Context, key string, reader io.Reader) error

	// Deletes an object from a bucket. It returns no error if the object does
	// not exist.
	Delete(ctx context.Context, key string) error

	// Copies an object identified by srcKey into destKey. srcKey should exist.
	// If destKey already exists, it should be replaced.
	Copy(ctx context.Context, srcKey, destKey string) error
}

// S3ObjectStorage implements ObjectStorage via an S3-compatible provider.
type S3ObjectStorage struct {
	// S3 SDK client.
	Client *s3.Client

	// S3 Bucket to put objects in.
	Bucket string

	// File extension to Content-Type map.
	ContentTypeMap map[string]string

	// Logger is used for reporting errors that cannot be handled and are
	// thrown away.
	Logger *slog.Logger
}

var _ ObjectStorage = (*S3ObjectStorage)(nil)

// S3StorageConfig holds the parameters needed to construct an S3ObjectStorage.
type S3StorageConfig struct {
	// (Required) S3 endpoint.
	Endpoint string

	// (Required) S3 region.
	Region string

	// (Required) S3 bucket.
	Bucket string

	// (Required) S3 access key ID.
	AccessKeyID string

	// (Required) S3 secret access key.
	SecretAccessKey string

	// File extension to Content-Type map.
	ContentTypeMap map[string]string

	// (Required) Logger is used for reporting errors that cannot be handled
	// and are thrown away.
	Logger *slog.Logger
}

// NewS3Storage constructs a new S3ObjectStorage.
func NewS3Storage(ctx context.Context, config S3StorageConfig) (*S3ObjectStorage, error) {
	storage := &S3ObjectStorage{
		Client: s3.New(s3.Options{
			BaseEndpoint: aws.String(config.Endpoint),
			Region:       config.Region,
			Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(config.AccessKeyID, config.SecretAccessKey, "")),
		}),
		Bucket:         config.Bucket,
		ContentTypeMap: config.ContentTypeMap,
		Logger:         config.Logger,
	}
	// Ping the bucket and see if we have access.
	_, err := storage.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  &storage.Bucket,
		MaxKeys: aws.Int32(1),
	})
	if err != nil {
		return nil, err
	}
	return storage, nil
}

// GetRange implements the GetRange ObjectStorage operation for
// S3ObjectStorage.
func (storage *S3ObjectStorage) GetRange(ctx context.Context, key string, wantRange [2]int64) (readCloser io.ReadCloser, gotRange [3]int64, err error) {
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Range
	var httpRange *string
	if wantRange != ([2]int64{}) {
		if wantRange[0] < 0 {
			return nil, [3]int64{}, &RangeNotSatisfiableError{Key: key, Start: wantRange[0], End: wantRange[1]}
		}
		var b strings.Builder
		b.WriteString("bytes=" + strconv.FormatInt(wantRange[0], 10) + "-")
		if wantRange[1] != 0 {
			if wantRange[1] < wantRange[0] {
				return nil, [3]int64{}, &RangeNotSatisfiableError{Key: key, Start: wantRange[0], End: wantRange[1]}
			}
			b.WriteString(strconv.FormatInt(wantRange[1], 10))
		}
		str := b.String()
		httpRange = &str
	}
	output, err := storage.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &storage.Bucket,
		Key:    aws.String(key),
		Range:  httpRange,
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			errorCode := apiErr.ErrorCode()
			switch errorCode {
			case "NoSuchKey":
				return nil, [3]int64{}, &fs.PathError{Op: "getrange", Path: key, Err: fs.ErrNotExist}
			case "InvalidRange":
				return nil, [3]int64{}, &RangeNotSatisfiableError{Key: key, Start: wantRange[0], End: wantRange[1], Size: -1}
			}
		}
		return nil, [3]int64{}, stacktrace.New(err)
	}
	if output.ContentRange == nil {
		return output.Body, [3]int64{}, nil
	}
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Range
	head, tail, _ := strings.Cut(*output.ContentRange, " ")
	if head != "bytes" {
		output.Body.Close()
		return nil, [3]int64{}, fmt.Errorf("S3 Object Storage returned unit other than bytes: %s", head)
	}
	tailHead, tailTail, _ := strings.Cut(strings.TrimSpace(tail), "/")
	if tailHead != "*" {
		rangeStart, rangeEnd, _ := strings.Cut(tailHead, "-")
		if n, err := strconv.ParseInt(rangeStart, 10, 64); err == nil {
			gotRange[0] = n
		}
		if n, err := strconv.ParseInt(rangeEnd, 10, 64); err == nil {
			gotRange[1] = n
		}
	}
	if tailTail != "*" {
		if n, err := strconv.ParseInt(tailTail, 10, 64); err == nil {
			gotRange[2] = n
		}
	}
	return output.Body, gotRange, nil
}

// Put implements the Put ObjectStorage operation for S3ObjectStorage.
func (storage *S3ObjectStorage) Put(ctx context.Context, key string, reader io.Reader) error {
	cleanup := func(uploadId *string) {
		_, err := storage.Client.AbortMultipartUpload(context.Background(), &s3.AbortMultipartUploadInput{
			Bucket:   &storage.Bucket,
			Key:      aws.String(key),
			UploadId: uploadId,
		})
		if err != nil {
			storage.Logger.Error(stacktrace.New(err).Error())
		}
	}
	contentType := storage.ContentTypeMap[strings.ToLower(path.Ext(key))]
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	createResult, err := storage.Client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:       &storage.Bucket,
		Key:          aws.String(key),
		CacheControl: aws.String("max-age=31536000, immutable" /* 1 year */),
		ContentType:  aws.String(contentType),
	})
	if err != nil {
		return stacktrace.New(err)
	}
	var parts []types.CompletedPart
	var partNumber int32
	var buf [5 << 20]byte
	done := false
	for !done {
		n, err := io.ReadFull(reader, buf[:])
		if err != nil {
			if err == io.EOF {
				break
			}
			if err != io.ErrUnexpectedEOF {
				cleanup(createResult.UploadId)
				return stacktrace.New(err)
			}
			done = true
		}
		partNumber++
		uploadResult, err := storage.Client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:     &storage.Bucket,
			Key:        aws.String(key),
			UploadId:   createResult.UploadId,
			PartNumber: aws.Int32(partNumber),
			Body:       bytes.NewReader(buf[:n]),
		})
		if err != nil {
			cleanup(createResult.UploadId)
			return stacktrace.New(err)
		}
		parts = append(parts, types.CompletedPart{
			ETag:       uploadResult.ETag,
			PartNumber: aws.Int32(partNumber),
		})
	}
	_, err = storage.Client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   &storage.Bucket,
		Key:      aws.String(key),
		UploadId: createResult.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: parts,
		},
	})
	if err != nil {
		return stacktrace.New(err)
	}
	return nil
}

// Delete implements the Delete ObjectStorage operation for S3ObjectStorage.
func (storage *S3ObjectStorage) Delete(ctx context.Context, key string) error {
	_, err := storage.Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &storage.Bucket,
		Key:    aws.String(key),
	})
	if err != nil {
		return stacktrace.New(err)
	}
	return nil
}

// Copy implements the Copy ObjectStorage operation for S3ObjectStorage.
func (storage *S3ObjectStorage) Copy(ctx context.Context, srcKey, destKey string) error {
	_, err := storage.Client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     &storage.Bucket,
		CopySource: aws.String(storage.Bucket + "/" + srcKey),
		Key:        aws.String(destKey),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "NoSuchKey" {
				return &fs.PathError{Op: "copy", Path: srcKey, Err: fs.ErrNotExist}
			}
		}
		return stacktrace.New(err)
	}
	return nil
}

// DirectoryObjectStorage implements ObjectStorage using a local directory.
type DirectoryObjectStorage struct {
	// Root directory to store objects in.
	RootDir string
}

// NewDirObjectStorage constructs a new DirectoryObjectStorage.
func NewDirObjectStorage(rootDir, tempDir string) (*DirectoryObjectStorage, error) {
	var err error
	rootDir, err = filepath.Abs(filepath.FromSlash(rootDir))
	if err != nil {
		return nil, err
	}
	directoryObjectStorage := &DirectoryObjectStorage{
		RootDir: filepath.FromSlash(rootDir),
	}
	return directoryObjectStorage, nil
}

// Get implements the Get ObjectStorage operation for DirectoryObjectStorage.
func (storage *DirectoryObjectStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}
	if len(key) < 4 {
		return nil, &fs.PathError{Op: "get", Path: key, Err: fs.ErrInvalid}
	}
	file, err := os.Open(filepath.Join(storage.RootDir, key[:4], key))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &fs.PathError{Op: "get", Path: key, Err: fs.ErrNotExist}
		}
		return nil, stacktrace.New(err)
	}
	return file, nil
}

// A LimitedReaderCloser reads from ReadCloser but limits the amount of data
// returned to just N bytes. Each call to Read updates N to reflect the new
// amount remaining. Read returns EOF when N <= 0 or when the underlying
// ReadCloser returns EOF.
//
// Its implementation is copied from io.LimitedReader.
type LimitedReaderCloser struct {
	ReadCloser io.ReadCloser
	N          int64
}

// Read reads from the underlying ReadCloser, but not beyond N bytes.
func (l *LimitedReaderCloser) Read(p []byte) (n int, err error) {
	if l.N <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > l.N {
		p = p[0:l.N]
	}
	n, err = l.ReadCloser.Read(p)
	l.N -= int64(n)
	return n, err
}

// Close closes the underlying ReadCloser.
func (l *LimitedReaderCloser) Close() error {
	return l.ReadCloser.Close()
}

// GetRange implements the GetRange ObjectStorage operation for
// DirectoryObjectStorage.
func (storage *DirectoryObjectStorage) GetRange(ctx context.Context, key string, wantRange [2]int64) (readCloser io.ReadCloser, gotRange [3]int64, err error) {
	err = ctx.Err()
	if err != nil {
		return nil, [3]int64{}, err
	}
	if len(key) < 4 {
		return nil, [3]int64{}, &fs.PathError{Op: "getrange", Path: key, Err: fs.ErrInvalid}
	}
	file, err := os.Open(filepath.Join(storage.RootDir, key[:4], key))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, [3]int64{}, &fs.PathError{Op: "getrange", Path: key, Err: fs.ErrNotExist}
		}
		return nil, [3]int64{}, stacktrace.New(err)
	}
	if wantRange == ([2]int64{}) {
		return file, [3]int64{}, nil
	}
	if wantRange[0] < 0 {
		return nil, [3]int64{}, &RangeNotSatisfiableError{Start: wantRange[0], End: wantRange[1]}
	}
	if wantRange[1] < wantRange[0] {
		return nil, [3]int64{}, &RangeNotSatisfiableError{Start: wantRange[0], End: wantRange[1]}
	}
	fileInfo, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, [3]int64{}, stacktrace.New(err)
	}
	gotRange[2] = fileInfo.Size()
	if wantRange[0] > 0 {
		if wantRange[0] > gotRange[2]-1 {
			return nil, [3]int64{}, &RangeNotSatisfiableError{Key: key, Start: wantRange[0], End: wantRange[1], Size: gotRange[2]}
		}
		gotRange[0], err = file.Seek(wantRange[0], io.SeekStart)
		if err != nil {
			file.Close()
			return nil, [3]int64{}, stacktrace.New(err)
		}
	}
	gotRange[1] = min(wantRange[1], gotRange[2]-1)
	readCloser = &LimitedReaderCloser{
		ReadCloser: file,
		N:          gotRange[1] - gotRange[0] + 1,
	}
	return readCloser, gotRange, nil
}

// Put implements the Put ObjectStorage operation for DirectoryObjectStorage.
func (storage *DirectoryObjectStorage) Put(ctx context.Context, key string, reader io.Reader) error {
	err := ctx.Err()
	if err != nil {
		return err
	}
	if len(key) < 4 {
		return &fs.PathError{Op: "put", Path: key, Err: fs.ErrInvalid}
	}
	file, err := os.OpenFile(filepath.Join(storage.RootDir, key[:4], key), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return stacktrace.New(err)
		}
		err = os.Mkdir(filepath.Join(storage.RootDir, key[:4]), 0755)
		if err != nil && !errors.Is(err, fs.ErrExist) {
			return stacktrace.New(err)
		}
		file, err = os.OpenFile(filepath.Join(storage.RootDir, key[:4], key), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return stacktrace.New(err)
		}
	}
	_, err = io.Copy(file, reader)
	if err != nil {
		return stacktrace.New(err)
	}
	return nil
}

// Delete implements the Delete ObjectStorage operation for
// DirectoryObjectStorage.
func (storage *DirectoryObjectStorage) Delete(ctx context.Context, key string) error {
	err := ctx.Err()
	if err != nil {
		return err
	}
	if len(key) < 4 {
		return &fs.PathError{Op: "delete", Path: key, Err: fs.ErrInvalid}
	}
	err = os.Remove(filepath.Join(storage.RootDir, key[:4], key))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return stacktrace.New(err)
	}
	return nil
}

// Copy implements the Copy ObjectStorage operation for DirectoryObjectStorage.
func (storage *DirectoryObjectStorage) Copy(ctx context.Context, srcKey, destKey string) error {
	err := ctx.Err()
	if err != nil {
		return err
	}
	if len(srcKey) < 4 {
		return &fs.PathError{Op: "copy", Path: srcKey, Err: fs.ErrInvalid}
	}
	if len(destKey) < 4 {
		return &fs.PathError{Op: "copy", Path: destKey, Err: fs.ErrInvalid}
	}
	srcFile, err := os.Open(filepath.Join(storage.RootDir, srcKey[:4], srcKey))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &fs.PathError{Op: "copy", Path: srcKey, Err: fs.ErrNotExist}
		}
		return stacktrace.New(err)
	}
	defer srcFile.Close()
	destFile, err := os.OpenFile(filepath.Join(storage.RootDir, destKey[:4], destKey), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return stacktrace.New(err)
	}
	defer destFile.Close()
	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		return stacktrace.New(err)
	}
	err = destFile.Close()
	if err != nil {
		return stacktrace.New(err)
	}
	return nil
}
