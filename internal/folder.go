package drivefs

import (
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

func (gdrive *Gdrive) CreateFolderRecursive(path string) (*drive.File, error) {
	if cacheNode, err := gdrive.Cache.Get(path); err == nil && cacheNode != nil {
		return cacheNode, nil
	}

	current, err, seq := gdrive.RootDrive, error(nil), GdrivePath(path).SplitPathSeq()
	for folderPath := range seq {
		previus := current // storage previus Node
		if current, err = gdrive.Cache.Get(folderPath); err == nil && current != nil {
			continue // continue to next node
		} else if current, err = gdrive.ResolveNode(previus.Id, folderPath); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, err // return drive error
			}

			// Base to create folder
			nodeCreate := &drive.File{
				MimeType: GoogleDriveMimeFolder, // folder mime
				Parents:  []string{previus.Id},  // previus to folder to create
			}

			// Create recursive folder
			for folderPath, name := range seq {
				nodeCreate.Name = name // folder name

				driveService, err := gdrive.PoolServervice.Get()
				if err != nil {
					return nil, err
				}

				if current, err = driveService.Files.Create(nodeCreate).Fields("*").Do(); err != nil {
					return nil, ProcessErr(nil, err)
				}

				gdrive.Cache.Set(DefaultTTL, folderPath, current) // Cache folder path
				nodeCreate.Parents[0] = current.Id                // Set new root
			}
			// Break loop and return current node
			break
		}

		// cache folder if not seted in cache
		gdrive.Cache.Set(DefaultTTL, folderPath, current)
	}
	return current, nil
}

func (gdrive *Gdrive) ResolveNode(folderID, name string) (*drive.File, error) {
	driveService, err := gdrive.PoolServervice.Get()
	if err != nil {
		return nil, err
	}

	name = strings.ReplaceAll(strings.ReplaceAll(name, `\`, `\\`), `'`, `\'`)
	file, err := driveService.Files.List().Fields("*").PageSize(300).Q(fmt.Sprintf(GoogleListQueryWithName, folderID, name)).Do()
	if err != nil {
		return nil, ProcessErr(nil, err)
	}

	if len(file.Files) != 1 {
		return nil, fs.ErrNotExist
	} else if file.Files[0].Trashed {
		return file.Files[0], fs.ErrNotExist
	}
	return file.Files[0], nil
}

func (gdrive *Gdrive) GetNode(name string) (current *drive.File, err error) {
	if GdrivePath(name).IsRoot() {
		return gdrive.RootDrive, nil
	}

	if current, err = gdrive.Cache.Get(name); err == nil && current != nil {
		return current, nil
	}

	current = gdrive.RootDrive            // root
	nodes := GdrivePath(name).SplitPath() // split node
	for _, currentNode := range nodes {
		previus := current // storage previus Node
		if current, err = gdrive.Cache.Get(currentNode[0]); err == nil && current != nil {
			continue // continue to next node
		}

		var err error
		// Check if ared exist in folder
		if current, err = gdrive.ResolveNode(previus.Id, currentNode[1]); err != nil {
			return nil, err // return drive error
		}
		gdrive.Cache.Set(DefaultTTL, currentNode[0], current)
	}
	return current, nil
}

func (gdrive *Gdrive) CreateNodeFolder(path string) (folderNode *drive.File, err error) {
	if cacheNode, _ := gdrive.Cache.Get(path); cacheNode != nil {
		return cacheNode, nil
	}

	nodes := GdrivePath(path).SplitPath()
	previusNode, lastNode := nodes.At(-2), nodes.At(-1)

	if folderNode, _ = gdrive.Cache.Get(previusNode.Path()); folderNode == nil {
		if folderNode, err = gdrive.ResolveNode(gdrive.RootDrive.Id, previusNode.Path()); err != nil {
			return
		}
		gdrive.Cache.Set(DefaultTTL, previusNode.Path(), folderNode)
	}

	previus := folderNode
	if folderNode, err = gdrive.ResolveNode(gdrive.RootDrive.Id, lastNode.Path()); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err // return drive error
		}

		nodeCreate := &drive.File{
			Name:     lastNode.Name(),       // Folder name
			MimeType: GoogleDriveMimeFolder, // folder mime
			Parents:  []string{previus.Id},  // previus to folder to create
		}

		driveService, err := gdrive.PoolServervice.Get()
		if err != nil {
			return nil, err
		}

		if folderNode, err = driveService.Files.Create(nodeCreate).Fields("*").Do(); err != nil {
			return nil, ProcessErr(nil, err)
		}
	}
	gdrive.Cache.Set(DefaultTTL, lastNode.Path(), folderNode)
	return
}

func (gdrive *Gdrive) ForwardPathResolve(nodeID string) (string, error) {
	pathNodes, fistNode, currentNode, err := []string{}, (*drive.File)(nil), (*drive.File)(nil), error(nil)
	for {
		driveService, err := gdrive.PoolServervice.Get()
		if err != nil {
			return "", err
		} else if currentNode, err = driveService.Files.Get(nodeID).Fields("*").Do(); err != nil {
			break
		}

		// Loop to check if is shortcut
		for limit := 200_000; limit > 0 && currentNode.MimeType == GoogleDriveMimeSyslink; limit-- {
			if driveService, err = gdrive.PoolServervice.Get(); err != nil {
				return "", err
			} else if currentNode, err = driveService.Files.Get(currentNode.ShortcutDetails.TargetId).Fields("*").Do(); err != nil {
				break
			}
		}

		parents := len(currentNode.Parents)
		if parents == 0 {
			break // Stop count
		} else if parents > 1 {
			parentsNode, node := []*drive.File{}, (*drive.File)(nil)
			for _, parentID := range currentNode.Parents {
				if driveService, err = gdrive.PoolServervice.Get(); err != nil {
					return "", err
				} else if node, err = driveService.Files.Get(parentID).Fields("*").Do(); err != nil {
					break
				}
				parentsNode = append(parentsNode, node)
			}
			slices.SortFunc(parentsNode, func(i, j *drive.File) int {
				ia, _ := time.Parse(time.RFC3339, i.CreatedTime)
				ja, _ := time.Parse(time.RFC3339, j.CreatedTime)
				return ia.Compare(ja)
			})
			currentNode = parentsNode[0]
		}

		if currentNode.Parents[0] == gdrive.RootDrive.Id {
			break // Break loop
		}
		nodeID = currentNode.Parents[0]                 // set new nodeID
		pathNodes = append(pathNodes, currentNode.Name) // Append name to path
		if fistNode == nil {
			fistNode = currentNode
		}

		// Save path to cache
		gdrive.Cache.Set(DefaultTTL, path.Join(Reverse(pathNodes)...), currentNode)
	}

	nodePath := path.Join(Reverse(pathNodes)...)
	gdrive.Cache.Set(DefaultTTL, nodePath, fistNode) // Save path to cache

	return nodePath, err
}

func Reverse[S ~[]E, E any](s S) S {
	n := len(s)
	reversedSlice := make([]E, n)
	if n == 0 {
		return reversedSlice // Return the empty slice right away
	}
	lastIndex := n - 1
	for i, element := range s {
		reversedSlice[lastIndex-i] = element
	}
	return reversedSlice
}

// Get file stream, if error check if is http2 error to make new request
func (gdrive *Gdrive) FileRequest(id string, s ...googleapi.Field) (*http.Response, error) {
	driveService, err := gdrive.PoolServervice.Get()
	if err != nil {
		return nil, err
	}

	quota := 0

	node := driveService.Files.Get(id).Fields(s...).AcknowledgeAbuse(true)
	res, err := node.Download()
	for i := 0; i < 3 && err != nil; i++ {
		err = ProcessErr(res, err)
		switch err {
		case fs.ErrNotExist, fs.ErrPermission:
			return nil, err
		case ErrQuota:
			if quota++; quota > 15 {
				<-time.After(time.Second * 20)
				quota = 0
			}
			if driveService, err = gdrive.PoolServervice.Next(); err != nil {
				return nil, err
			}
			node = driveService.Files.Get(id).Fields(s...).AcknowledgeAbuse(true)
			res, err = node.Download()
		case ErrHttp2:
			<-time.After(time.Microsecond * 2) // Wait seconds to retry download, to google server close connection
			res, err = node.Download()
		default:
			return res, err
		}
	}
	return res, err
}

func (gdrive *Gdrive) ListFiles(folderID string) iter.Seq2[*drive.File, error] {
	return func(yield func(*drive.File, error) bool) {
		driveService, err := gdrive.PoolServervice.Get()
		if err != nil {
			yield(nil, ProcessErr(nil, err))
			return
		}

		folder := driveService.Files.List().Fields("*").Q(fmt.Sprintf(GoogleListQuery, folderID)).PageSize(100_000)
		for {
			nodes, err := folder.Do()
			if err != nil {
				yield(nil, ProcessErr(nil, err))
				return
			}

			for _, node := range nodes.Files {
				if !yield(node, nil) {
					return
				}
			}

			if folder.PageToken(nodes.NextPageToken); nodes.NextPageToken == "" {
				break
			}
		}
	}
}
