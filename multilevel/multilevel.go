package multilevel

import (
	"fmt"
	"github.com/tgulacsi/go-cdb"
	"github.com/tgulacsi/go-locking"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const MaxCdbSize = 1 << 30 //1Gb

// compacts path directory, if number of cdb files greater than threshold
func Compact(path string, threshold int) error {
	if locks, err := locking.FLockDirs(path); err != nil {
		return err
	} else {
		defer locks.Unlock()
	}
	files, err := listDir(path, 'S', func(fi os.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), ".cdb")
	})
	if err != nil {
		return fmt.Errorf("cannot list dir %s: %s", path, err)
	}
	if len(files) < threshold {
		return nil
	}
	size := int64(0)
	bucket := make([]string, 0, 16)
	for _, fi := range files {
		fs := fi.Size()
		if fs+size > MaxCdbSize {
			if err = MergeCdbs(time.Now().Format(time.RFC3339), bucket...); err != nil {
				return fmt.Errorf("error merging cdbs (%s): %s", strings.Join(bucket, ", "), err)
			}
			size = 0
			bucket = bucket[:0]
		}
		bucket = append(bucket, filepath.Join(path, fi.Name()))
	}
	return nil
}

// merges the cdbs, dumping filenames to newfn
func MergeCdbs(newfn string, filenames ...string) error {
	fh, err := os.Create(newfn)
	if err != nil {
		return err
	}
	defer fh.Close()
	donech := make(chan error, len(filenames))
	eltch := make(chan cdb.Element, len(filenames)|16)
	errch := make(chan error, 1)
	go func(w io.WriteSeeker) {
		errch <- cdb.MakeFromChan(w, eltch)
	}(fh)
	for _, fn := range filenames {
		ifh, err := os.Open(fn)
		if err != nil {
			return fmt.Errorf("cannot open %s: %s", fn, err)
		}
		go func(r io.Reader) {
			donech <- cdb.DumpToChan(eltch, r)
		}(ifh)
	}
	for i := 0; i < len(filenames); i++ {
		select {
		case err = <-errch:
			if err != nil {
				return fmt.Errorf("error making %s: %s", fh, err)
			}
			i--
		case err = <-donech:
			if err != nil {
				return fmt.Errorf("error dumping: %s", err)
				i--
			}
		}
	}
	close(eltch)
	err = <-errch
	if err != nil {
		return fmt.Errorf("error while making %s: %s", fh, err)
	}
	for _, fn := range filenames {
		os.Remove(fn)
	}
	return nil
}

type fileInfos []os.FileInfo

func listDir(path string, orderBy byte, filter func(os.FileInfo) bool) (files fileInfos, err error) {
	dh, e := os.Open(path)
	if e != nil {
		err = fmt.Errorf("cannot open dir %s: %s", path, e)
		return
	}
	defer dh.Close()

	files = make(fileInfos, 0, 16)
	for {
		flist, e := dh.Readdir(1024)
		if e == io.EOF {
			break
		} else if e != nil {
			err = fmt.Errorf("erro listing dir %s: %s", dh, e)
			return
		}
		for _, fi := range flist {
			if filter(fi) {
				files = append(files, fi)
			}
		}
	}

	switch orderBy {
	case 's':
		sort.Sort(growingSize(files))
	case 'S':
		sort.Sort(shrinkingSize(files))
	}
	return
}

// for sorting by growing size
type growingSize fileInfos

func (gfi growingSize) Len() int           { return len(gfi) }
func (gfi growingSize) Swap(i, j int)      { gfi[i], gfi[j] = gfi[j], gfi[i] }
func (gfi growingSize) Less(i, j int) bool { return gfi[i].Size() < gfi[j].Size() }

// for sorting by shrinking size
type shrinkingSize fileInfos

func (gfi shrinkingSize) Len() int           { return len(gfi) }
func (gfi shrinkingSize) Swap(i, j int)      { gfi[i], gfi[j] = gfi[j], gfi[i] }
func (sfi shrinkingSize) Less(i, j int) bool { return sfi[i].Size() > sfi[j].Size() }