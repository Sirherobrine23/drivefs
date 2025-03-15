package drivefs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"google.golang.org/api/drive/v3"
)

func escapeName(n string) string {
	return strings.Join(strings.Split(n, "/"), "%%2f")
}

// Extends [*google.golang.org/api/drive/v3.File]
type Stat struct{ File *drive.File }

func (node Stat) Sys() any     { return node.File }
func (node Stat) Name() string { return escapeName(path.Clean(node.File.Name)) }
func (node Stat) Size() int64  { return node.File.Size }
func (node Stat) IsDir() bool  { return node.File.MimeType == GoogleDriveMimeFolder }
func (node Stat) Mode() fs.FileMode {
	switch node.File.MimeType {
	case GoogleDriveMimeFolder:
		return fs.ModeDir | fs.ModePerm
	case GoogleDriveMimeSyslink:
		return fs.ModeSymlink | fs.ModePerm
	default:
		return fs.ModePerm
	}
}

func (node Stat) ModTime() time.Time {
	t := time.Date(0, 0, 0, 0, 0, 0, 0, time.UTC)
	for _, fileTime := range []string{node.File.ModifiedTime, node.File.CreatedTime} {
		if fileTime == "" {
			continue
		}
		t.UnmarshalText([]byte(fileTime))
		break
	}
	return t
}

var (
	_ File    = (*GdriveNode)(nil)
	_ fs.File = (File)(nil)

	ErrInvalidOffset error = errors.New("Seek: invalid offset")
)

type File interface {
	io.ReadWriteCloser
	io.ReaderFrom
	io.WriterTo
	Stat() (fs.FileInfo, error)
	ReadDir(count int) ([]fs.DirEntry, error)
}

const (
	DirectionWait   Direction = iota // Wait to read or write
	DirectionWrite                   // Only accept write
	DirectionReader                  // Only accept reader
)

type Direction int

type ErrInvalidDirection Direction

func (err ErrInvalidDirection) Error() string {
	switch Direction(err) {
	case DirectionReader:
		return "cannot access write with direction is writer"
	case DirectionWrite:
		return "cannot write with direction is reader"
	case DirectionWait:
		return "cannot write or reader without open fist file"
	}
	return "unknown direction"
}

type GdriveNode struct {
	filename  string         // Filename path
	gClient   *Gdrive        // Client setuped
	node      *drive.File    // File node
	nodeRoot  *drive.File    // root to create file
	sRead     *io.PipeReader // Pipe reader
	sWrite    *io.PipeWriter // Pipe writer
	offset    int64          // File offset
	sReadRes  *http.Response // read http response if is read operation
	direction Direction      // File direction

	// Files in node

	filesOffset int           // current count
	nodeFiles   []fs.DirEntry // files node
}

func (node GdriveNode) Stat() (fs.FileInfo, error) {
	if node.node == nil {
		return nil, fs.ErrNotExist
	}
	return &Stat{File: node.node}, nil
}

func (node *GdriveNode) ReadFrom(r io.Reader) (n int64, err error) {
	if len(node.nodeFiles) > 0 || node.filesOffset > 0 {
		return 0, &fs.PathError{Op: "readfrom", Path: node.filename, Err: fs.ErrInvalid}
	}
	pathNodes := node.gClient.pathSplit(node.filename)
	if !(node.direction == DirectionWrite || node.direction == DirectionWait) {
		return 0, fs.ErrInvalid
	}

	// Copy from current offset
	if node.direction == DirectionWrite {
		return io.Copy(node, r)
	}

	rootSolver := node.gClient.rootDrive
	if node.node == nil && node.nodeRoot == nil {
		if node.gClient.checkMkdir(node.filename) {
			if rootSolver, err = node.gClient.mkdirAllNodes(pathNodes[len(pathNodes)-2].Path); err != nil {
				return 0, err
			}
		}

		if rootSolver, err = node.gClient.driveService.Files.Create(&drive.File{MimeType: "application/octet-stream", Name: pathNodes[len(pathNodes)-1].Name, Parents: []string{rootSolver.Id}}).Fields("*").Media(r).Do(); err != nil {
			return 0, err
		}
		node.node = rootSolver // set new node
	} else if node.node == nil && node.nodeRoot != nil {
		if rootSolver, err = node.gClient.driveService.Files.Create(&drive.File{MimeType: "application/octet-stream", Name: pathNodes[len(pathNodes)-1].Name, Parents: []string{node.nodeRoot.Id}}).Fields("*").Media(r).Do(); err != nil {
			return 0, err
		}
		node.node = rootSolver // set new node
	} else if rootSolver, err = node.gClient.driveService.Files.Update(node.node.Id, nil).Media(r).Do(); err != nil {
		return 0, err
	}

	node.gClient.cachePut(pathNodes[len(pathNodes)-1].Path, rootSolver)
	return rootSolver.Size, nil
}

func (node *GdriveNode) WriteTo(w io.Writer) (n int64, err error) {
	if len(node.nodeFiles) > 0 || node.filesOffset > 0 {
		return 0, &fs.PathError{Op: "writeto", Path: node.filename, Err: fs.ErrInvalid}
	}
	if node.node == nil {
		return 0, fs.ErrNotExist
	} else if !(node.direction == DirectionReader || node.direction == DirectionWait) {
		return 0, fs.ErrInvalid
	}

	// Write from current offset
	if node.direction == DirectionReader {
		return io.Copy(w, node)
	}

	res, err := node.gClient.getRequest(node.gClient.driveService.Files.Get(node.node.Id))
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	return io.Copy(w, res.Body)
}

func (node *GdriveNode) Close() error {
	switch node.direction {
	case DirectionWrite:
		if node.sWrite != nil {
			if err := node.sWrite.Close(); err != nil {
				return err
			}
		}
	case DirectionReader:
		if node.sReadRes != nil {
			if err := node.sReadRes.Body.Close(); err != nil {
				return err
			}
		}
		if node.sRead != nil {
			if err := node.sRead.Close(); err != nil {
				return err
			}
		}
	}
	node.direction = DirectionWait
	return nil
}

func (node *GdriveNode) Read(p []byte) (n int, err error) {
	if len(node.nodeFiles) > 0 || node.filesOffset > 0 {
		return 0, &fs.PathError{Op: "read", Path: node.filename, Err: fs.ErrInvalid}
	}
	err = io.EOF // default error
	switch node.direction {
	case DirectionWrite:
		return 0, ErrInvalidDirection(DirectionReader)
	case DirectionReader:
		if node.sReadRes != nil {
			n, err = node.sReadRes.Body.Read(p)
		} else if node.sRead != nil {
			n, err = node.sRead.Read(p)
		}
	case DirectionWait:
		if node.node == nil {
			return 0, io.ErrUnexpectedEOF
		}
		if node.sReadRes, err = node.gClient.getRequest(node.gClient.driveService.Files.Get(node.node.Id)); err != nil {
			return 0, err
		}
		node.direction = DirectionReader
		n, err = node.sReadRes.Body.Read(p)
	}

	node.offset += int64(n)
	return
}

func (node *GdriveNode) Write(p []byte) (n int, err error) {
	if len(node.nodeFiles) > 0 || node.filesOffset > 0 {
		return 0, &fs.PathError{Op: "write", Path: node.filename, Err: fs.ErrInvalid}
	}
	err = io.EOF // default error
	switch node.direction {
	case DirectionReader:
		return 0, ErrInvalidDirection(DirectionWrite)
	case DirectionWrite:
		if node.sWrite != nil {
			n, err = node.sWrite.Write(p)
		}
	case DirectionWait:
		node.direction = DirectionWrite
		pathNodes := node.gClient.pathSplit(node.filename)
		nodeID := ""

		if node.node == nil && node.nodeRoot == nil {
			if node.gClient.checkMkdir(node.filename) {
				if node.nodeRoot, err = node.gClient.mkdirAllNodes(pathNodes[len(pathNodes)-2].Path); err != nil {
					return 0, err
				}
			}
		}
		if node.node == nil {
			node.sRead, node.sWrite = io.Pipe()
			if node.node, err = node.gClient.driveService.Files.Create(&drive.File{MimeType: "application/octet-stream", Name: pathNodes[len(pathNodes)-1].Name, Parents: []string{node.nodeRoot.Id}}).Fields("*").Media(bytes.NewReader([]byte{})).Do(); err != nil {
				return 0, err
			}
		}

		nodeID = node.node.Id
		go node.gClient.driveService.Files.Update(nodeID, nil).Media(node.sRead).Do()
		n, err = node.sWrite.Write(p)
	}

	node.offset += int64(n) // append new offset
	return
}

func (node *GdriveNode) Seek(offset int64, whence int) (of int64, err error) {
	if len(node.nodeFiles) > 0 || node.filesOffset > 0 {
		return 0, &fs.PathError{Op: "seek", Path: node.filename, Err: fs.ErrInvalid}
	}
	switch node.direction {
	case DirectionWait:
		if !(whence == io.SeekStart || whence == io.SeekCurrent) {
			return 0, io.ErrUnexpectedEOF
		} else if offset < 0 {
			return 0, &fs.PathError{Op: "seek", Path: node.filename, Err: fs.ErrInvalid}
		}
		node.offset = offset
		return offset, nil
	case DirectionReader:
		switch whence {
		case io.SeekCurrent:
			return io.CopyN(io.Discard, node, offset)
		case io.SeekStart:
			if offset < 0 || offset > node.node.Size {
				return 0, &fs.PathError{Op: "seek", Path: node.filename, Err: fs.ErrInvalid}
			}

			// Close current body
			if node.sReadRes != nil {
				if err = node.sReadRes.Body.Close(); err != nil {
					return 0, &fs.PathError{Op: "seek", Path: node.filename, Err: err}
				}
				node.sReadRes = nil
			}

			fileCall := node.gClient.driveService.Files.Get(node.node.Id)
			if offset > 0 {
				fileCall.Header().Set("Range", fmt.Sprintf("bytes=%d-%d", offset, node.node.Size-1))
			}

			if node.sReadRes, err = node.gClient.getRequest(fileCall); err != nil {
				return 0, err
			}
			node.offset = offset
		case io.SeekEnd:
			newOffset := node.node.Size - offset
			if newOffset < 0 {
				return 0, &fs.PathError{Op: "seek", Path: node.filename, Err: fs.ErrInvalid}
			}

			fileCall := node.gClient.driveService.Files.Get(node.node.Id)
			fileCall.Header().Set("Range", fmt.Sprintf("bytes=%d-%d", newOffset, node.node.Size-1))
			if node.sReadRes, err = node.gClient.getRequest(fileCall); err != nil {
				return 0, &fs.PathError{Op: "seek", Path: node.filename, Err: err}
			}
			node.offset = newOffset
			return newOffset, nil
		}
	case DirectionWrite:
		switch whence {
		case io.SeekCurrent:
			of = 0
			for offset > 0 {
				buffSize := min(4028, offset)
				offset -= buffSize
				n, err := node.sWrite.Write(make([]byte, buffSize))
				if err != nil {
					return 0, &fs.PathError{Op: "seek", Path: node.filename, Err: err}
				}
				of += int64(n)
			}
		case io.SeekStart, io.SeekEnd:
			return 0, &fs.PathError{Op: "seek", Path: node.filename, Err: fs.ErrInvalid}
		}
	}
	return 0, io.EOF
}

// cannot list files nodes
func (node *GdriveNode) ReadDir(count int) (entrys []fs.DirEntry, err error) {
	if len(node.nodeFiles) == 0 && node.filesOffset == 0 {
		return nil, &fs.PathError{Op: "readdir", Path: node.filename, Err: fs.ErrInvalid}
	} else if len(node.nodeFiles) == 0 {
		return nil, io.EOF
	} else if count == -1 {
		entrys = node.nodeFiles
		node.nodeFiles = nil
		return
	}

	count = min(len(node.nodeFiles), count)
	entrys = node.nodeFiles[:count]
	node.nodeFiles = node.nodeFiles[count:]
	return
}
