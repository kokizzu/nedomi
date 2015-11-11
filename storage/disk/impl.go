package disk

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/ironsmile/nedomi/config"
	"github.com/ironsmile/nedomi/types"
	"github.com/ironsmile/nedomi/utils"
)

// Disk implements the Storage interface by writing data to a disk
type Disk struct {
	partSize           uint64
	path               string
	dirPermissions     os.FileMode
	filePermissions    os.FileMode
	logger             types.Logger
	skipCacheKeyInPath bool
}

// PartSize the maximum part size for the disk storage.
func (s *Disk) PartSize() uint64 {
	return s.partSize
}

// GetMetadata returns the metadata on disk for this object, if present.
func (s *Disk) GetMetadata(id *types.ObjectID) (*types.ObjectMetadata, error) {
	//!TODO: optimize - reading and parsing the file from disk every time is very ineffictient
	s.logger.Debugf("[DiskStorage] Getting metadata for %s...", id)
	return s.getObjectMetadata(s.getObjectMetadataPath(id))
}

// GetPart returns an io.ReadCloser that will read the specified part of the
// object from the disk.
func (s *Disk) GetPart(idx *types.ObjectIndex) (io.ReadCloser, error) {
	s.logger.Debugf("[DiskStorage] Getting file data for %s...", idx)
	f, err := os.Open(s.getObjectIndexPath(idx))
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		return nil, utils.NewCompositeError(err, f.Close())
	}

	if uint64(stat.Size()) > s.partSize {
		err = fmt.Errorf("Object part has invalid size %d", stat.Size())
		return nil, utils.NewCompositeError(err, f.Close(), s.DiscardPart(idx))
	}

	return f, nil
}

// GetAvailableParts returns types.ObjectIndexMap including all the available
// parts of for the object specified by the provided objectMetadata
func (s *Disk) GetAvailableParts(oid *types.ObjectID) ([]*types.ObjectIndex, error) {
	files, err := ioutil.ReadDir(s.getObjectIDPath(oid))
	if err != nil {
		return nil, err
	}

	parts := make([]*types.ObjectIndex, 0, len(files))
	for _, f := range files {
		if f.Name() == objectMetadataFileName {
			continue
		}

		//!TODO: do not return error for unknown filenames? they could be downloads in progress
		partNum, err := s.getPartNumberFromFile(f.Name())
		if err != nil {
			return nil, fmt.Errorf("Wrong part file for %s: %s", oid, err)
		} else if uint64(f.Size()) > s.partSize {
			return nil, fmt.Errorf("Part file %d for %s has incorrect size %d", partNum, oid, f.Size())
		} else {
			parts = append(parts, &types.ObjectIndex{
				ObjID: oid,
				Part:  partNum,
			})
		}
	}

	return parts, nil
}

// SaveMetadata writes the supplied metadata to the disk.
func (s *Disk) SaveMetadata(m *types.ObjectMetadata) error {
	s.logger.Debugf("[DiskStorage] Saving metadata for %s...", m.ID)

	tmpPath := appendRandomSuffix(s.getObjectMetadataPath(m.ID))
	f, err := s.createFile(tmpPath)
	if err != nil {
		return err
	}

	if err = json.NewEncoder(f).Encode(m); err != nil {
		return utils.NewCompositeError(err, f.Close())
	} else if err := f.Close(); err != nil {
		return err
	}

	//!TODO: use a faster encoding than json (some binary marshaller? gob?)

	return os.Rename(tmpPath, s.getObjectMetadataPath(m.ID))
}

// SavePart writes the contents of the supplied object part to the disk.
func (s *Disk) SavePart(idx *types.ObjectIndex, data io.Reader) error {
	s.logger.Debugf("[DiskStorage] Saving file data for %s...", idx)

	tmpPath := appendRandomSuffix(s.getObjectIndexPath(idx))
	f, err := s.createFile(tmpPath)
	if err != nil {
		return err
	}

	if savedSize, err := io.Copy(f, data); err != nil {
		return utils.NewCompositeError(err, f.Close(), os.Remove(tmpPath))
	} else if uint64(savedSize) > s.partSize {
		err = fmt.Errorf("Object part has invalid size %d", savedSize)
		return utils.NewCompositeError(err, f.Close(), os.Remove(tmpPath))
	} else if err := f.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.getObjectIndexPath(idx))
}

// Discard removes the object and its metadata from the disk.
func (s *Disk) Discard(id *types.ObjectID) error {
	s.logger.Debugf("[DiskStorage] Discarding %s...", id)
	oldPath := s.getObjectIDPath(id)
	tmpPath := appendRandomSuffix(oldPath)
	if err := os.Rename(oldPath, tmpPath); err != nil {
		return err
	}

	return os.RemoveAll(tmpPath)
}

// DiscardPart removes the specified part of an Object from the disk.
func (s *Disk) DiscardPart(idx *types.ObjectIndex) error {
	s.logger.Debugf("[DiskStorage] Discarding %s...", idx)
	return os.Remove(s.getObjectIndexPath(idx))
}

// Iterate is a disk-specific function that iterates over all the objects on the
// disk and passes them to the supplied callback function. If the callback
// function returns false, the iteration stops.
func (s *Disk) Iterate(callback func(*types.ObjectMetadata, ...*types.ObjectIndex) bool) error {
	// At most count(cacheKeys)*256*256 directories
	rootDirs, err := filepath.Glob(s.path + s.iterateGlob())
	if err != nil {
		return err
	}

	//!TODO: should we delete the offending folder if we detect an error? maybe just in some cases?
	for _, rootDir := range rootDirs {
		//TODO: stat dirs little by little?
		objectDirs, err := ioutil.ReadDir(rootDir)
		if err != nil {
			return err
		}

		for _, objectDir := range objectDirs {
			objectDirPath := filepath.Join(rootDir, objectDir.Name(), objectMetadataFileName)
			//!TODO: continue on os.ErrNotExist, delete on other errors?
			obj, err := s.getObjectMetadata(objectDirPath)
			if err != nil {
				s.logger.Errorf(
					"[DiskStorage] error on getting metadata from %s - %s",
					objectDirPath, err)
				continue
			}
			parts, err := s.GetAvailableParts(obj.ID)
			if err != nil {
				s.logger.Errorf(
					"[DiskStorage] error on getting parts from %s - %s",
					objectDirPath, err)
				continue
			}
			if !callback(obj, parts...) {
				return nil
			}
		}
	}
	return nil
}

// New returns a new disk storage that ready for use.
func New(cfg *config.CacheZone, log types.Logger) (*Disk, error) {
	if cfg == nil || log == nil {
		return nil, fmt.Errorf("Nil constructor parameters")
	}

	if cfg.PartSize == 0 {
		return nil, fmt.Errorf("Invalid partSize value")
	}

	if _, err := os.Stat(cfg.Path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Disk storage path `%s` should be created.", cfg.Path)
		}
		return nil, fmt.Errorf("Cannot stat the disk storage path %s: %s", cfg.Path, err)
	}

	s := &Disk{
		partSize:           cfg.PartSize.Bytes(),
		path:               cfg.Path,
		dirPermissions:     0700 | os.ModeDir, //!TODO: get from the config
		filePermissions:    0600,              //!TODO: get from the config
		logger:             log,
		skipCacheKeyInPath: cfg.SkipCacheKeyInPath,
	}

	return s, s.saveSettingsOnDisk(cfg)
}

// ChangeConfig change the logger of the disk storage
func (s *Disk) ChangeConfig(log types.Logger) {
	s.logger = log
}

const (
	skipKeyIterateGlob = "/[0-9a-f][0-9a-f]/[0-9a-f][0-9a-f]"
	withKeyIterateGlob = "/*/[0-9a-f][0-9a-f]/[0-9a-f][0-9a-f]"
)

func (s *Disk) iterateGlob() string {
	if s.skipCacheKeyInPath {
		return skipKeyIterateGlob
	}
	return withKeyIterateGlob
}
