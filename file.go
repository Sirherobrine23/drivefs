package drivefs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"sync"
	"syscall"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

var (
	_ fs.FileInfo = (*NodeStat)(nil)
	_ File        = (*DirNode)(nil)
	_ File        = (*FileNode)(nil)

	_ fs.FS         = (*Gdrive)(nil)
	_ fs.StatFS     = (*Gdrive)(nil)
	_ fs.ReadDirFS  = (*Gdrive)(nil)
	_ fs.ReadFileFS = (*Gdrive)(nil)
	_ fs.SubFS      = (*Gdrive)(nil)
)

// Extends [*google.golang.org/api/drive/v3.File]
type NodeStat struct{ File *drive.File }

func (node *NodeStat) Sys() any    { return node.File }
func (node NodeStat) Name() string { return pathManipulate(path.Clean(node.File.Name)).EscapeName() }
func (node NodeStat) Size() int64  { return node.File.Size }
func (node NodeStat) IsDir() bool  { return node.File.MimeType == GoogleDriveMimeFolder }
func (node NodeStat) Mode() fs.FileMode {
	if mode, ok := node.File.Properties[UnixModeProperties]; ok {
		if mod, err := strconv.ParseUint(mode, 10, 64); err == nil {
			return fs.FileMode(mod)
		}
	}

	switch node.File.MimeType {
	case GoogleDriveMimeFolder:
		return fs.ModeDir | 0666
	case GoogleDriveMimeSyslink:
		return fs.ModeSymlink | 0777
	}

	return 0777 | fs.ModeType
}

func (node NodeStat) ModTime() time.Time {
	for _, fileTime := range []string{node.File.ModifiedTime, node.File.CreatedTime} {
		if fileTime != "" {
			if t, err := time.ParseInLocation(time.RFC3339, fileTime, time.UTC); err == nil {
				return t
			}
		}
	}
	return time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC)
}

func convertDriveToDir(input []*drive.File) (out []fs.DirEntry) {
	out = make([]fs.DirEntry, 0)
	for _, data := range input {
		out = append(out, fs.FileInfoToDirEntry(&NodeStat{File: data}))
	}
	return
}

// Representation to Dir entrys and non regular file
type DirNode struct {
	Node *drive.File

	Offset int           // current count
	Files  []*drive.File // files node
}

// Remote File Read and Write directly without with basic At Read and write
type FileNode struct {
	Node       *drive.File
	NodeUpdate *drive.FilesUpdateCall

	Writer io.WriteCloser
	Reader io.ReadCloser
	Client *Gdrive
	Locker *sync.Mutex

	Offset int64 // Offset
}

type LocalFile struct {
	*os.File // Local file append to struct
}

func (*DirNode) Sync() error                                    { return nil }
func (*DirNode) Truncate(size int64) error                      { return io.EOF }
func (*DirNode) Read([]byte) (int, error)                       { return 0, io.EOF }
func (*DirNode) ReadAt(p []byte, off int64) (n int, err error)  { return 0, io.EOF }
func (*DirNode) Write(p []byte) (n int, err error)              { return 0, io.EOF }
func (*DirNode) WriteAt(p []byte, off int64) (n int, err error) { return 0, io.EOF }

func (dir *DirNode) Close() error               { dir.Offset = -1; return nil }
func (dir *DirNode) Stat() (fs.FileInfo, error) { return &NodeStat{File: dir.Node}, nil }

func (dir *DirNode) Seek(offset int64, whence int) (int64, error) {
	if dir.Offset < 0 || len(dir.Files) >= dir.Offset {
		return 0, io.EOF
	}

	switch whence {
	case io.SeekStart:
		if offset < 0 {
			return 0, fs.ErrInvalid
		} else if offset > int64(len(dir.Files)) {
			return 0, io.ErrUnexpectedEOF
		}
		dir.Offset = int(offset)
	case io.SeekCurrent:
		offset = int64(dir.Offset) + offset
		if offset > int64(len(dir.Files)) || offset < 0 {
			return 0, io.ErrUnexpectedEOF
		}
		dir.Offset += int(offset)
	case io.SeekEnd:
		if offset > 0 {
			offset = ^offset
		}
		dir.Offset = len(dir.Files) + int(offset)
	}

	return int64(dir.Offset), nil
}

func (dir *DirNode) ReadDir(count int) ([]fs.DirEntry, error) {
	if dir.Offset < 0 || len(dir.Files) >= dir.Offset {
		return nil, io.EOF
	} else if count < 0 {
		dir.Offset = -1
		return convertDriveToDir(dir.Files), nil
	}
	min := min(count, len(dir.Files[dir.Offset:]))
	dir.Offset += min
	return convertDriveToDir(dir.Files[dir.Offset : dir.Offset+min]), nil
}

func (*FileNode) Sync() error                              { return nil }
func (*FileNode) ReadDir(count int) ([]fs.DirEntry, error) { return nil, fs.ErrInvalid }
func (*FileNode) Truncate(size int64) error                { return syscall.ECONNREFUSED }

func (file *FileNode) Stat() (fs.FileInfo, error) { return &NodeStat{File: file.Node}, nil }
func (file *FileNode) Close() error {
	var closed io.Closer
	switch {
	case file.Reader != nil:
		closed = file.Reader
	case file.Writer != nil:
		closed = file.Writer
	default:
		return nil // nothing to close
	}
	file.Offset = -1
	file.Reader = nil
	file.Writer = nil
	file.Client = nil
	return closed.Close()
}

func (file *FileNode) Read(p []byte) (int, error)        { return file.ReadAt(p, file.Offset) }
func (file *FileNode) Write(p []byte) (n int, err error) { return file.WriteAt(p, file.Offset) }

// Update offset
func (file *FileNode) Seek(offset int64, whence int) (int64, error) {
	if file.Reader == nil || file.Writer == nil || file.Client == nil || file.Offset < 0 {
		return 0, io.EOF
	}

	switch whence {
	case io.SeekStart:
		if offset < 0 {
			return 0, fs.ErrInvalid
		} else if offset > file.Node.Size {
			return 0, io.ErrUnexpectedEOF
		}
		file.Offset = offset
	case io.SeekCurrent:
		offset = file.Offset + offset
		if offset > file.Node.Size || offset < 0 {
			return 0, io.ErrUnexpectedEOF
		}
		file.Offset += offset
	case io.SeekEnd:
		if offset > 0 {
			offset = ^offset
		}
		file.Offset = file.Node.Size + offset
	default:
		return 0, &fs.PathError{Op: "seek", Path: file.Node.Id, Err: fs.ErrInvalid}
	}

	return file.Offset, nil
}

// Read is Seek and Read in same function
func (file *FileNode) ReadAt(p []byte, off int64) (n int, err error) {
	if file.Writer == nil && file.Reader == nil {
		return 0, io.EOF
	} else if file.Writer != nil && file.Reader == nil {
		return 0, fs.ErrInvalid
	} else if off < 0 || off > file.Node.Size {
		return 0, io.ErrUnexpectedEOF
	}

	switch min(1, max(-1, file.Offset-off)) {
	case 1: // Discart next reader
		file.Locker.Lock()
		defer file.Locker.Unlock()

		if _, err = io.CopyN(io.Discard, file.Reader, file.Offset-off); err != nil {
			return 0, ProcessErr(nil, err)
		}
		file.Offset = off
	case -1: // Restart body reader
		file.Locker.Lock()
		defer file.Locker.Unlock()

		// Set current offset off file
		reopenFile := file.Client.driveService.Files.Get(file.Node.Id)
		reopenFile.Header().Set("Range", fmt.Sprintf("bytes=%d-", off))

		res, err := openFileAPI(reopenFile)
		if err != nil {
			return 0, ProcessErr(httpRes(res), err)
		}

		// Close if opened header
		if file.Reader != nil {
			file.Reader.Close()
		}

		// Add to Reader field
		file.Reader = res.Body
		file.Offset = off
	}

	n, err = file.Reader.Read(p)
	file.Offset += int64(n)
	if err == io.EOF {
		file.Close()
	}
	return
}

func (file *FileNode) WriteAt(p []byte, off int64) (n int, err error) {
	if file.Writer == nil && file.Reader == nil {
		return 0, io.EOF
	}

	if file.Writer != nil && file.Reader != nil {
		go file.Client.driveService.Files.Update(file.Node.Id, nil).Media(file.Reader, googleapi.ContentType("application/octet-stream")).Do()
		file.Reader = nil // Remove from struct
	}

	if file.Writer == nil && file.Reader != nil {
		return 0, syscall.EINVAL
	}

	// Start media writer
	if updater := file.NodeUpdate; updater != nil {
		file.NodeUpdate = nil
		go updater.Do()
	}

	if file.Offset == off { // direct write
		n, err = file.Writer.Write(p)
		file.Offset += int64(n)
		if err == io.EOF {
			file.Close()
		}
		return
	}

	// return invalid error
	return 0, errors.Join(errors.New("WriteAt not support to change offset"), fs.ErrInvalid)
}
