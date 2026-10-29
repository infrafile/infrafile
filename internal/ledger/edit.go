package ledger

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var editID = regexp.MustCompile(`^[a-f0-9]{64}$`)

// CheckEdit prevents queries from presenting a partly written transaction as committed inventory.
func CheckEdit(dir string) error {
	info, err := os.Lstat(filepath.Join(dir, ".infrafile"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.IsDir() {
		return errors.New(".infrafile 必须是普通目录")
	}
	root, err := os.OpenRoot(filepath.Join(dir, ".infrafile"))
	if err != nil {
		return err
	}
	defer root.Close()
	active, err := root.Lstat("active")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !active.Mode().IsRegular() || active.Size() != 65 {
		return errors.New("台账变更状态损坏；请运行 change status/recover")
	}
	f, err := root.Open("active")
	if err != nil {
		return err
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(active, opened) {
		return errors.New("活动事务标记已变化")
	}
	b, err := io.ReadAll(io.LimitReader(f, 66))
	if err != nil || len(b) != 65 || b[64] != '\n' || !editID.Match(b[:64]) {
		return errors.New("台账变更状态损坏；请运行 change status/recover")
	}
	// Only the transaction's own commit hooks may validate the completed candidate before publication.
	if allowed := os.Getenv("INFRAFILE_EDIT_READ"); editID.MatchString(allowed) && bytes.Equal(b, []byte(allowed+"\n")) {
		return nil
	}
	return errors.New("台账存在未完成变更；请运行 change status/recover，完成后再查询")
}
