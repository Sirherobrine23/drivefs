package drivefs

import (
	"io"
	"io/fs"
	"path/filepath"
	"time"

	"google.golang.org/api/drive/v3"
)

type FileNode struct {
	Node   *drive.File `json:"node"`
	Childs []*FileNode `json:"childs"`
}

func (node *FileNode) UnixPath() []string {
	n := []string{node.Node.Name}
	for index := range node.Childs {
		k := node.Childs[index].UnixPath()
		for nodeIndex := range k {
			n = append(n, filepath.Join(node.Node.Name, k[nodeIndex]))
		}
	}
	return n
}

func (node *FileNode) ReverseNode(path string) *FileNode {
	spath := filepath.SplitList(path)
	if len(spath) == 0 {
		return node
	}

	for index := range node.Childs {
		if node.Childs[index].Node.Name == spath[0] {
			if data := node.Childs[index].ReverseNode(filepath.Join(spath[1:]...)); data != nil {
				return data
			}
		}
	}

	return nil
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

func (gfs *FsFile) Name() string {
	return gfs.node.Node.Name
}
func (gfs *FsFile) Size() int64 {
	return gfs.node.Node.Size
}
func (gfs *FsFile) IsDir() bool {
	return gfs.node.Node.MimeType == MimeFolder
}
func (gfs *FsFile) Sys() any {
	return gfs.node.Node
}
func (gfs *FsFile) ModTime() time.Time {
	t, _ := time.Parse(time.RFC3339, gfs.node.Node.ModifiedTime)
	return t
}
func (gfs *FsFile) Mode() fs.FileMode {
	var n fs.FileMode = 0755
	if gfs.node.Node.OwnedByMe {
		n = 0777
	} else if gfs.node.Node.Capabilities.CanEdit && gfs.node.Node.Capabilities.CanDelete {
		n = 0757
	}
	return n
}
func (gfs *FsFile) Type() fs.FileMode {
	if gfs.node.Node.MimeType == MimeFolder {
		return fs.ModeDir
	} else if gfs.node.Node.MimeType == MimeSyslink {
		return fs.ModeSymlink
	}
	return gfs.Mode()
}
func (gfs *FsFile) Info() (fs.FileInfo, error) {
	return gfs.Stat()
}
func (gfs *FsFile) Stat() (fs.FileInfo, error) {
	return gfs, nil
}

func (gfs *FsFile) Close() error {
	if gfs.original != nil {
		return gfs.original.Close()
	}
	return nil
}

func (gfs *FsFile) Read(p []byte) (int, error) {
	if gfs.original == nil {
		res, err := gfs.client.Files.Get(gfs.node.Node.Id).AcknowledgeAbuse(true).Download()
		if err != nil {
			return 0, err
		}
		gfs.original = res.Body
	}
	return gfs.original.Read(p)
}
