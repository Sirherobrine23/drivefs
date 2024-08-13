package drivefs

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"google.golang.org/api/drive/v3"
)

const (
	MimeFolder  string = "application/vnd.google-apps.folder"   // Folder mimetype
	MimeSyslink string = "application/vnd.google-apps.shortcut" // Syslink/hardlink
)

type DriveEntry struct {
	driveFile *drive.File
}

type DriveOpen struct {
	driveFile   *drive.File
	driveClient *drive.Service

	driveRead *http.Response
}

type DriveInfo struct {
	driveFile *drive.File
	modTime   time.Time
}

func fsInfo(driveFile *drive.File, driveClient *drive.Service) fs.File {
	return &DriveOpen{driveFile, driveClient, nil}
}

func fsInfoDir(driveFile *drive.File) fs.DirEntry {
	return &DriveEntry{driveFile}
}

func fsStat(driveFile *drive.File) (fs.FileInfo, error) {
	var err error
	var modTime time.Time

	if modTime, err = time.Parse(time.RFC3339, driveFile.ModifiedTime); err != nil {
		return nil, err
	}
	return &DriveInfo{driveFile, modTime}, nil
}

func getNodePermission(driveFile *drive.File) fs.FileMode {
	var n fs.FileMode = 0755
	if driveFile.OwnedByMe {
		n = 0777
	} else if driveFile.Capabilities.CanEdit && driveFile.Capabilities.CanDelete {
		n = 0757
	}
	return n
}

func getNodeType(driveFile *drive.File) fs.FileMode {
	if driveFile.MimeType == MimeFolder {
		return fs.ModeDir
	} else if driveFile.MimeType == MimeSyslink {
		return fs.ModeSymlink
	}
	return getNodePermission(driveFile)
}

func (entry *DriveEntry) Name() string                 { return entry.driveFile.Name }
func (entry *DriveEntry) IsDir() bool                  { return entry.driveFile.MimeType == MimeFolder }
func (entry *DriveEntry) Type() fs.FileMode            { return getNodeType(entry.driveFile) }
func (entry *DriveEntry) Info() (fs.FileInfo, error)   { return fsStat(entry.driveFile) }
func (entry *DriveEntry) MarshalJSON() ([]byte, error) {
	return json.MarshalIndent(&drive.File{Name: entry.Name(), Size: entry.driveFile.Size, ModifiedTime: entry.driveFile.ModifiedTime}, "", "  ")
}

func (entry *DriveInfo) MarshalText() ([]byte, error) { return []byte(entry.Name()), nil }
func (entry *DriveInfo) Name() string                 { return entry.driveFile.Name }
func (entry *DriveInfo) Size() int64                  { return entry.driveFile.Size }
func (entry *DriveInfo) Mode() fs.FileMode            { return getNodeType(entry.driveFile) }
func (entry *DriveInfo) ModTime() time.Time           { return entry.modTime }
func (entry *DriveInfo) IsDir() bool                  { return entry.driveFile.MimeType == MimeFolder }
func (entry *DriveInfo) Sys() any                     { return nil }

func (file *DriveOpen) Stat() (fs.FileInfo, error) { return fsStat(file.driveFile) }
func (file *DriveOpen) Close() error {
	if file.driveRead == nil {
		return nil
	}
	return file.driveRead.Body.Close()
}
func (file *DriveOpen) Read(p []byte) (int, error) {
	if file.driveRead == nil {
		var err error
		if file.driveRead, err = file.driveClient.Files.Get(file.driveFile.Id).Download(); err != nil {
			return 0, err
		}
	}
	return file.driveRead.Body.Read(p)
}
