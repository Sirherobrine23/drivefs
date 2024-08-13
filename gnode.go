package drivefs

import (
	"io"
	"io/fs"
	"time"

	"google.golang.org/api/drive/v3"
)

type FileNode struct {
	File *drive.File
}

func (node *FileNode) FsInfo(client *drive.Service) fs.File {
	return &FsFile{client: client, node: node}
}
func (node *FileNode) FsInfoDir(client *drive.Service) fs.DirEntry {
	return &FsFile{client: client, node: node}
}

type FsFile struct {
	original io.ReadCloser
	node     *FileNode
	client   *drive.Service
}

func (gfs *FsFile) MarshalText() ([]byte, error) {
	return []byte(gfs.node.File.Name), nil
}

func (gfs *FsFile) Name() string {
	return gfs.node.File.Name
}
func (gfs *FsFile) Size() int64 {
	return gfs.node.File.Size
}
func (gfs *FsFile) IsDir() bool {
	return gfs.node.File.MimeType == MimeFolder
}

func (gfs *FsFile) ModTime() time.Time {
	t, _ := time.Parse(time.RFC3339, gfs.node.File.ModifiedTime)
	return t
}

func (gfs *FsFile) Sys() any { return nil }

func (gfs *FsFile) Mode() fs.FileMode {
	var n fs.FileMode = 0755
	if gfs.node.File.OwnedByMe {
		n = 0777
	} else if gfs.node.File.Capabilities.CanEdit && gfs.node.File.Capabilities.CanDelete {
		n = 0757
	}
	return n
}

func (gfs *FsFile) Type() fs.FileMode {
	if gfs.node.File.MimeType == MimeFolder {
		return fs.ModeDir
	} else if gfs.node.File.MimeType == MimeSyslink {
		return fs.ModeSymlink
	}
	return gfs.Mode()
}

func (gfs *FsFile) Stat() (fs.FileInfo, error) {
	return gfs, nil
}
func (gfs *FsFile) Info() (fs.FileInfo, error) {
	return gfs.Stat()
}

func (gfs *FsFile) Close() error {
	if gfs.original != nil {
		return gfs.original.Close()
	}
	return nil
}

func (gfs *FsFile) Read(p []byte) (int, error) {
	if gfs.original == nil {
		res, err := gfs.client.Files.Get(gfs.node.File.Id).AcknowledgeAbuse(true).Download()
		if err != nil {
			return 0, err
		}
		gfs.original = res.Body
	}
	return gfs.original.Read(p)
}
