package api

import (
	"net/http"

	"fam/server/internal/model"
)

// snapshot GET /api/backup/snapshot
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := s.st.Snapshot()
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="fam-snapshot.json"`)
	writeJSON(w, http.StatusOK, snap)
}

// restore POST /api/backup/restore
func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode    string         `json:"mode"`
		Payload model.Snapshot `json:"payload"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Mode == "" {
		req.Mode = "replace"
	}
	if err := s.st.RestoreSnapshot(req.Payload, req.Mode); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":    req.Mode,
		"account": len(req.Payload.Accounts),
		"entry":   len(req.Payload.Entries),
	})
}

// backupFile POST /api/backup/file
func (s *Server) backupFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir string `json:"dir"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	path, err := s.st.BackupToFile(req.Dir)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path})
}

// restoreFile POST /api/backup/file/restore
func (s *Server) restoreFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Path == "" {
		badRequest(w, "备份文件路径不能为空")
		return
	}
	if err := s.st.RestoreFromFile(req.Path); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": req.Path, "restored": true})
}
