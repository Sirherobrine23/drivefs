package drivefs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"reflect"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/api/drive/v3"
)

var _ fs.File = &Open{}

type Open struct {
	node    *drive.File
	client  *Gdrive
	nodeRes *http.Response
	offset  int64
}

func (open *Open) Stat() (fs.FileInfo, error) { return Stat{open.node}, nil }

func (open *Open) Close() error {
	if open.nodeRes == nil || open.nodeRes.Body == nil {
		return nil
	}
	err := open.nodeRes.Body.Close()
	open.nodeRes = nil
	open.offset = 0
	return err
}

func (open *Open) Read(p []byte) (int, error) {
	if open.nodeRes == nil || open.nodeRes.Body == nil {
		node := open.client.driveService.Files.Get(open.node.Id).AcknowledgeAbuse(true)

		// Set Range from offset
		if open.offset > 0 && open.node.Size <= open.offset {
			node.Header().Set("Range", fmt.Sprintf("bytes=%d-%d", open.offset, open.node.Size-1))
		}

		// Start open request
		var err error
		if open.nodeRes, err = open.client.getRequest(node); err != nil {
			return 0, err
		}
	}

	n, err := open.nodeRes.Body.Read(p)
	if open.offset += int64(n); err != nil && err != io.EOF {
		return n, err
	}
	return n, err
}

func (open *Open) Seek(offset int64, whence int) (int64, error) {
	if offset < 0 {
		return 0, errors.New("Seek: invalid offset")
	} else if open.nodeRes == nil || open.nodeRes.Body == nil {
		return 0, io.EOF
	}

	switch whence {
	case io.SeekStart:
		if offset > open.node.Size {
			return 0, io.EOF
		}
		open.Close()
		node := open.client.driveService.Files.Get(open.node.Id).AcknowledgeAbuse(true)
		node.Header().Set("Range", fmt.Sprintf("bytes=%d-%d", offset, open.node.Size-1))
		var err error
		if open.nodeRes, err = open.client.getRequest(node); err != nil {
			return 0, err
		}
		open.offset = offset
	case io.SeekCurrent:
		newOffset := open.offset + offset
		if newOffset < 0 || newOffset > open.node.Size {
			return 0, io.EOF
		} else if _, err := io.CopyN(io.Discard, open, offset); err != nil {
			return 0, err
		}
		open.offset = newOffset
	case io.SeekEnd:
		newOffset := open.node.Size - offset
		if newOffset < 0 {
			return 0, io.EOF
		}
		open.Close()
		node := open.client.driveService.Files.Get(open.node.Id).AcknowledgeAbuse(true)
		node.Header().Set("Range", fmt.Sprintf("bytes=%d-%d", newOffset, open.node.Size-1))
		var err error
		if open.nodeRes, err = open.client.getRequest(node); err != nil {
			return 0, err
		}
		open.offset = newOffset
	default:
		return 0, fs.ErrInvalid
	}

	return open.offset, nil
}

// Get file stream, if error check if is http2 error to make new request
func (gdrive *Gdrive) getRequest(node *drive.FilesGetCall) (*http.Response, error) {
	res, err := node.Download()
	for i := 0; i < 10 && err != nil; i++ {
		if urlError, ok := err.(*url.Error); ok {
			if _, ok := urlError.Err.(http2.GoAwayError); ok || reflect.TypeOf(urlError.Err).String() == "http.http2GoAwayError" {
				<-time.After(time.Microsecond * 2) // Wait seconds to retry download, to google server close connection
				res, err = node.Download()
				continue
			} else if res != nil && res.StatusCode == 429 {
				<-time.After(time.Minute) // Wait minutes to reset www.google.com/sorry/index
				res, err = node.Download()
				continue
			}
		}
		break
	}
	return res, err
}

// resolve path and return File stream
func (gdrive *Gdrive) Open(path string) (fs.File, error) {
	fileNode, err := gdrive.getNode(path)
	if err != nil {
		return nil, err
	}
	boot, err := gdrive.getRequest(gdrive.driveService.Files.Get(fileNode.Id).AcknowledgeAbuse(true))
	if err != nil {
		return nil, err
	}
	return &Open{fileNode, gdrive, boot, 0}, nil
}

func (gdrive Gdrive) ReadFile(name string) ([]byte, error) {
	file, err := gdrive.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

// Create recursive directory if not exists
func (gdrive *Gdrive) MkdirAll(path string) (*drive.File, error) {
	var current *drive.File
	if current = gdrive.cacheGet(gdrive.fixPath(path)); current != nil {
		return current, nil
	}

	current = gdrive.rootDrive      // root
	nodes := gdrive.pathSplit(path) // split node
	for nodeIndex, currentNode := range nodes {
		previus := current // storage previus Node
		if current = gdrive.cacheGet(currentNode.Path); current != nil {
			continue // continue to next node
		}

		var err error
		// Check if ared exist in folder
		if current, err = gdrive.resolveNode(previus.Id, currentNode.Name); err != nil {
			if err != fs.ErrNotExist {
				return nil, err // return drive error
			}

			// Base to create folder
			var folderCreate drive.File
			folderCreate.MimeType = GoogleDriveMimeFolder // folder mime
			folderCreate.Parents = []string{previus.Id}   // previus to folder to create

			// Create recursive folder
			for _, currentNode = range nodes[nodeIndex:] {
				folderCreate.Name = currentNode.Name // folder name
				if current, err = gdrive.driveService.Files.Create(&folderCreate).Fields("*").Do(); err != nil {
					return nil, err
				}
				gdrive.cachePut(currentNode.Path, current)
				folderCreate.Parents[0] = current.Id // Set new root
			}

			// return new folder
			return current, nil
		}
		gdrive.cachePut(currentNode.Path, current)
	}
	return current, nil
}

func (gdrive *Gdrive) Delete(path string) error {
	fileNode, err := gdrive.getNode(path)
	if err != nil {
		return err
	}
	gdrive.cacheDelete(path)
	return gdrive.driveService.Files.Delete(fileNode.Id).Do()
}
