package api

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// GET /api/admin/backup — download a consistent snapshot of the database.
//
// The panel had no backup story at all: the only safe way to copy a WAL-mode
// SQLite database is from inside SQLite, so an admin following the obvious
// instinct (scp the .db) walks away with a torn file and does not find out
// until they try to restore it. See Store.BackupTo.
//
// The snapshot is a full copy of the database, so it contains everything an
// admin can already read through this API — including the encrypted columns.
// Those stay encrypted: restoring the file elsewhere needs the same
// QZ_SECRET_KEY, which is not in the database and not in this download.
func (a *API) handleAdminBackup(w http.ResponseWriter, r *http.Request) {
	// A private temp dir per request: two admins clicking at the same moment
	// must not race on one path, and VACUUM INTO refuses an existing file.
	dir, err := os.MkdirTemp("", "qingzhou-backup-")
	if err != nil {
		fail(w, http.StatusInternalServerError, "创建临时目录失败")
		return
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("backup: temp cleanup failed: %v", err)
		}
	}()

	name := "qingzhou-backup-" + time.Now().Format("20060102-150405") + ".db"
	path := filepath.Join(dir, name)
	if err := a.st.BackupTo(path); err != nil {
		log.Printf("backup: %v", err)
		fail(w, http.StatusInternalServerError, "生成备份失败")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取备份失败")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取备份失败")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", itoa(st.Size()))
	// A database dump must never sit in a proxy or browser cache.
	w.Header().Set("Cache-Control", "no-store")
	if _, err := io.Copy(w, f); err != nil {
		// Headers are already out; the client sees a truncated download, which
		// is the honest outcome. Log it so a recurring failure is visible.
		log.Printf("backup: send failed after %d bytes header: %v", st.Size(), err)
	}
}
