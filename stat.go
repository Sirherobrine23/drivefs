package drivefs

import (
	"io/fs"
	"path/filepath"
	"time"

	"google.golang.org/api/drive/v3"
)

type Stat struct {
	*drive.File
}

func (node Stat) Name() string { return filepath.Clean(node.File.Name) }
func (node Stat) Size() int64  { return node.File.Size }
func (node Stat) IsDir() bool  { return node.File.MimeType == GoogleDriveMimeFolder }

func (node Stat) Sys() any { return node.File }

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

func (node Stat) Mode() fs.FileMode {
	if node.File.MimeType == GoogleDriveMimeFolder {
		return fs.ModeDir | fs.ModePerm
	} else if node.File.MimeType == GoogleDriveMimeSyslink {
		return fs.ModeSymlink | fs.ModePerm
	}
	return fs.ModePerm
}

// Resolve path and return File or Folder Stat
func (gdrive *Gdrive) Stat(path string) (fs.FileInfo, error) {
	fileNode, err := gdrive.getNode(path)
	if err != nil {
		return nil, err
	}
	return &Stat{fileNode}, nil
}

// List files and folder in Directory
func (gdrive *Gdrive) ReadDir(name string) ([]fs.DirEntry, error) {
	current, err := (*drive.File)(nil), error(nil)
	if current, err = gdrive.getNode(name); err != nil {
		return nil, err
	}

	nodes, err := gdrive.listNodes(current.Id)
	if err != nil {
		return nil, err
	}

	entrysSlice := []fs.DirEntry{}
	for index := range nodes {
		entrysSlice = append(entrysSlice, fs.FileInfoToDirEntry(&Stat{nodes[index]}))
	}

	return entrysSlice, nil
}
