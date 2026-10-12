package server

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/config"
	"pocketadmin/internal/permission"
	"pocketadmin/internal/storage"
)

// storageResources declares the file library's managed collections.
func storageResources() []resource {
	return []resource{
		{
			Collection:  "file_folders",
			BasePath:    "/storage/folders",
			Title:       "Folders",
			Singular:    "folder",
			Active:      "storage.folders",
			ListPerm:    permission.StorageFolderList,
			CreatePerm:  permission.StorageFolderCreate,
			UpdatePerm:  permission.StorageFolderUpdate,
			DeletePerm:  permission.StorageFolderDelete,
			Order:       "path,sortOrder",
			Filter:      "deletedAt = ''",
			TreeView:    true,
			ParentField: "parent",
			// path and level are derived from the tree position by the folder
			// hooks, never entered. Without this a re-parent could not find the
			// subtree prefix and a crafted post could rewrite the path.
			DerivedFields: []string{"path", "level"},
			TreePathField: "path",
			LabelField:    "name",
			CountOf: &countSpec{
				Collection: "files",
				Field:      "folder",
				Label:      "Files",
				Filter:     "deletedAt = ''",
			},
			BeforeDelete: func(s *Server, rec *core.Record) error {
				if err := refuseIfReferenced(s, "file_folders", "parent", rec.Id,
					"This folder still has sub-folders. Move or delete them first."); err != nil {
					return err
				}
				return refuseIfReferenced(s, "files", "folder", rec.Id,
					"This folder still holds files. Move or delete them first.")
			},
		},
		{
			Collection: "files",
			BasePath:   "/storage/files",
			Title:      "Library",
			Singular:   "file",
			Active:     "storage.files",
			ListPerm:   permission.StorageFileList,
			// Creation happens through the upload screen, which writes the bytes
			// as well as the row, so the generic create form is not offered.
			CreatePerm: "",
			UpdatePerm: permission.StorageFileUpdate,
			DeletePerm: permission.StorageFileDelete,
			Order:      "-created",
			Filter:     "deletedAt = ''",
			LabelField: "name",
			// size, checksum, storedName, mimeType and extension are all established
			// by reading the bytes. Letting a form overwrite them would let a record
			// claim a size or type its content does not have.
			DerivedFields: []string{"size", "checksum", "storedName", "mimeType", "extension", "kind"},
			BeforeDelete:  guardFileDelete,
		},
	}
}

// registerStorageRoutes mounts the library.
func (s *Server) registerStorageRoutes(panel *gin.RouterGroup) {
	for _, r := range storageResources() {
		s.resourceRoutes(panel, r)
	}

	s.readGuarded(panel, http.MethodGet, "/storage/upload", permission.StorageFileUpload, s.handleUploadForm)
	s.guarded(panel, http.MethodPost, "/storage/files/upload", permission.StorageFileUpload, s.handleUpload)
	s.readGuarded(panel, http.MethodGet, "/storage/files/:id/download", permission.StorageFileDownload, s.handleDownload)
	s.readGuarded(panel, http.MethodGet, "/storage/files/:id/preview", permission.StorageFileDownload, s.handlePreview)

	// Attachments are how a library file is referenced from elsewhere. Detaching
	// is what makes the reference count fall.
	s.readGuarded(panel, http.MethodGet, "/storage/files/:id/references", permission.StorageAttachmentList, s.handleFileReferences)
	s.guarded(panel, http.MethodPost, "/storage/attachments/:id/detach", permission.StorageAttachmentManage, s.handleAttachmentDetach)
}

// storageDriver returns the configured driver, or nil when storage is not usable.
func (s *Server) storageDriver() storage.Driver { return s.files }

// ---------------------------------------------------------------------------
// Upload
// ---------------------------------------------------------------------------

// handleUploadForm renders the upload screen.
func (s *Server) handleUploadForm(c *gin.Context) {
	s.render(c, http.StatusOK, "storage_upload.html", uploadPage{
		base:    s.newBase(c, "Upload files", "storage.files"),
		Folders: s.relationOptionsFor("file_folders", "name"),
		Folder:  strings.TrimSpace(c.Query("folder")),
		Policy:  s.uploadPolicy(),
	})
}

// handleUpload stores one or more uploaded files.
//
// Each file is inspected before anything is written: the content type is sniffed
// from the bytes, the checksum is computed, and the policy is applied to the
// sniffed type rather than to the extension the client supplied. Only then is the
// object stored, and only if an object with that checksum is not already present,
// which is what makes deduplication free.
func (s *Server) handleUpload(c *gin.Context) {
	driver := s.storageDriver()
	if driver == nil {
		s.setFlash(c, "danger", "No storage driver is configured.")
		c.Redirect(http.StatusSeeOther, "/storage/upload")
		return
	}

	form, err := c.MultipartForm()
	if err != nil {
		s.setFlash(c, "danger", "Could not read the upload: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/storage/upload")
		return
	}

	headers := form.File["file"]
	if len(headers) == 0 {
		s.setFlash(c, "warning", "No file was selected.")
		c.Redirect(http.StatusSeeOther, "/storage/upload")
		return
	}

	folderID := strings.TrimSpace(c.PostForm("folder"))
	policy := s.uploadPolicy()
	actor := s.actor(c)
	col, err := s.app.FindCollectionByNameOrId("files")
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", err.Error())
		return
	}

	var stored, deduped int
	var problems []string

	for _, header := range headers {
		file, err := header.Open()
		if err != nil {
			problems = append(problems, header.Filename+": could not be read")
			continue
		}

		if policy.MaxSize > 0 && header.Size > policy.MaxSize {
			_ = file.Close()
			problems = append(problems, fmt.Sprintf("%s: larger than the %d byte limit",
				storage.SafeName(header.Filename), policy.MaxSize))
			continue
		}

		inspected, err := storage.Inspect(file)
		_ = file.Close()
		if err != nil {
			problems = append(problems, storage.SafeName(header.Filename)+": "+err.Error())
			continue
		}

		if !policy.Allows(inspected.Extension) {
			problems = append(problems, fmt.Sprintf("%s: %s is not an allowed file type",
				storage.SafeName(header.Filename), describeType(inspected)))
			continue
		}

		key := storage.Key(inspected.Checksum)

		// The object may already exist because another row addresses the same
		// content. That is the deduplication: the bytes are written once and both
		// rows point at them.
		exists, err := driver.Exists(key)
		if err != nil {
			problems = append(problems, storage.SafeName(header.Filename)+": "+err.Error())
			continue
		}

		if !exists {
			// Re-open: Inspect consumed the stream.
			again, err := header.Open()
			if err != nil {
				problems = append(problems, storage.SafeName(header.Filename)+": could not be re-read")
				continue
			}
			_, saveErr := driver.Save(key, again)
			_ = again.Close()
			if saveErr != nil {
				problems = append(problems, storage.SafeName(header.Filename)+": "+saveErr.Error())
				continue
			}
			stored++
		} else {
			deduped++
		}

		rec := core.NewRecord(col)
		rec.Set("name", storage.SafeName(header.Filename))
		rec.Set("storedName", key)
		rec.Set("folder", folderID)
		rec.Set("size", inspected.Size)
		rec.Set("mimeType", inspected.MimeType)
		rec.Set("extension", inspected.Extension)
		rec.Set("checksum", inspected.Checksum)
		rec.Set("storage", "local")
		rec.Set("scope", "private")
		rec.Set("status", "active")
		rec.Set("tags", []string{inspected.Kind})
		rec.Set("refCount", 0)
		if actor != nil && !actor.IsSuperuser {
			rec.Set("owner", actor.UserID)
		}

		if err := s.app.Save(rec); err != nil {
			problems = append(problems, storage.SafeName(header.Filename)+": "+strings.Join(validationMessages(err), "; "))
			continue
		}

		s.audit(c, auditEntry{
			Event:      "storage.file_upload",
			TargetType: "file",
			TargetID:   rec.Id,
			Summary:    "Uploaded " + rec.GetString("name") + " (" + inspected.MimeType + ")",
			Detail: map[string]any{
				"size": inspected.Size, "checksum": inspected.Checksum, "deduplicated": exists,
			},
		})
	}

	switch {
	case len(problems) > 0:
		s.setFlash(c, "warning", fmt.Sprintf(
			"Stored %d file(s), reused %d existing object(s). Problems: %s",
			stored, deduped, strings.Join(problems, "; ")))
	default:
		s.setFlash(c, "success", fmt.Sprintf(
			"Stored %d file(s) and reused %d existing object(s).", stored, deduped))
	}

	c.Redirect(http.StatusSeeOther, "/storage/files")
}

// describeType names the sniffed type for an error message, preferring the
// extension so the message is about what the file is rather than its MIME string.
func describeType(inspected storage.Inspected) string {
	if inspected.Extension != "" {
		return "a " + strings.TrimPrefix(inspected.Extension, ".") + " file (" + inspected.MimeType + ")"
	}
	return inspected.MimeType
}

// uploadPolicy returns the active policy.
//
// Per D-012 the values belong to the settings module, which is a later wave, so
// they come from configuration with conservative defaults until then. The mapping
// lives here rather than in config so that config does not have to know about the
// storage package.
func (s *Server) uploadPolicy() storage.Policy {
	policy := storage.DefaultPolicy()
	if s.cfg.UploadMaxSize > 0 {
		policy.MaxSize = s.cfg.UploadMaxSize
	}
	if len(s.cfg.UploadAllowedExtensions) > 0 {
		policy.AllowedExtensions = s.cfg.UploadAllowedExtensions
	}
	return policy
}

// ---------------------------------------------------------------------------
// Download and preview
// ---------------------------------------------------------------------------

// handleDownload streams a file's bytes as an attachment.
func (s *Server) handleDownload(c *gin.Context) {
	s.serveFile(c, true)
}

// handlePreview streams a file's bytes inline, for types that are safe to render.
func (s *Server) handlePreview(c *gin.Context) {
	s.serveFile(c, false)
}

// serveFile streams a stored object.
//
// The content type is the one that was sniffed at upload time, never anything the
// client sends now, and it is sent with nosniff so a browser cannot decide a
// stored file is something else. Non-images are forced to download: rendering a
// stored file inline is how an uploaded document becomes a script running in the
// panel's origin.
func (s *Server) serveFile(c *gin.Context, asAttachment bool) {
	driver := s.storageDriver()
	if driver == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Storage unavailable",
			"No storage driver is configured.")
		return
	}

	rec, ok := s.findRecord(c, mustCollection(s.app, "files"))
	if !ok {
		return
	}

	if rec.GetString("status") != "active" {
		s.renderErrorPage(c, http.StatusForbidden, "Unavailable", "That file is not available.")
		return
	}

	key := rec.GetString("storedName")
	if key == "" {
		s.renderErrorPage(c, http.StatusInternalServerError, "Unavailable",
			"That file has no stored object.")
		return
	}

	reader, err := driver.Open(key)
	if err != nil {
		slog.Error("could not open a stored file", "file", rec.Id, "key", key, "error", err)
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "The stored object is missing.")
		return
	}
	defer reader.Close()

	mimeType := rec.GetString("mimeType")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	inline := !asAttachment && strings.HasPrefix(mimeType, "image/") &&
		mimeType != "image/svg+xml"

	c.Header("Content-Type", mimeType)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Header("Cache-Control", "private, max-age=300")

	// An SVG is an image that can carry script, so it downloads rather than
	// rendering, even on the preview route.
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	c.Header("Content-Disposition", disposition+`; filename="`+sanitiseHeaderValue(rec.GetString("name"))+`"`)

	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, reader)
}

// sanitiseHeaderValue removes what must not appear in a header value.
func sanitiseHeaderValue(v string) string {
	v = strings.ReplaceAll(v, `"`, "")
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", "")
	if v == "" {
		return "file"
	}
	return v
}

// ---------------------------------------------------------------------------
// Deletion and references
// ---------------------------------------------------------------------------

// guardFileDelete refuses to delete a file that something still references.
//
// The reference count is maintained from the attachments table rather than
// trusted from the stored column, because a count that drifts is a file that
// silently disappears from under a record that still points at it.
func guardFileDelete(s *Server, rec *core.Record) error {
	count, err := s.countRecords(mustCollection(s.app, "attachments"),
		"file = {:id}", map[string]any{"id": rec.Id})
	if err != nil {
		return nil
	}
	if count > 0 {
		return fmt.Errorf("this file is referenced by %d record(s). Detach it first.", count)
	}
	return nil
}

// removeStoredObject removes a file's bytes once its row is gone, unless another
// row still addresses the same object.
//
// Two rows can share one object, because identical content computes the same key.
// Removing the key while another row still points at it would leave that row
// reading a file that is not there, so the check happens after the row is gone --
// at which point "is anyone else using this key" is the only question left.
func removeStoredObject(s *Server, rec *core.Record) {
	key := rec.GetString("storedName")
	if key == "" {
		return
	}

	others, err := s.app.FindRecordsByFilter("files",
		"storedName = {:k}", "", 1, 0, map[string]any{"k": key})
	if err == nil && len(others) > 0 {
		// Still in use; leave the bytes alone.
		return
	}

	driver := s.storageDriver()
	if driver == nil {
		return
	}
	if err := driver.Delete(key); err != nil {
		slog.Error("could not remove a stored object", "key", key, "error", err)
	}
}

// handleFileReferences lists what points at a file.
func (s *Server) handleFileReferences(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, "files"))
	if !ok {
		return
	}

	refs, err := s.app.FindRecordsByFilter("attachments",
		"file = {:id}", "-created", 0, 0, map[string]any{"id": rec.Id})
	if err != nil {
		refs = nil
	}

	s.render(c, http.StatusOK, "storage_references.html", referencesPage{
		base:        s.newBase(c, "References", "storage.files"),
		File:        rec,
		Attachments: refs,
		CanDetach:   s.actor(c).Can(permission.StorageAttachmentManage),
	})
}

// handleAttachmentDetach removes one reference and brings the file's stored count
// back in line with reality.
func (s *Server) handleAttachmentDetach(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, "attachments"))
	if !ok {
		return
	}

	fileID := rec.GetString("file")
	if err := s.app.Delete(rec); err != nil {
		s.setFlash(c, "danger", "Could not detach: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/storage/files")
		return
	}

	s.syncRefCount(fileID)

	s.setFlash(c, "success", "Attachment detached.")
	c.Redirect(http.StatusSeeOther, "/storage/files/"+fileID+"/references")
}

// syncRefCount recomputes a file's reference count from the attachments table.
//
// It is recomputed rather than decremented: a count maintained by increment and
// decrement drifts the first time a write fails between the two, and a count that
// is too high makes a file undeletable.
func (s *Server) syncRefCount(fileID string) {
	if fileID == "" {
		return
	}

	rec, err := s.app.FindRecordById("files", fileID)
	if err != nil || rec == nil {
		return
	}

	count, err := s.countRecords(mustCollection(s.app, "attachments"),
		"file = {:id}", map[string]any{"id": fileID})
	if err != nil {
		return
	}

	if rec.GetInt("refCount") == int(count) {
		return
	}
	rec.Set("refCount", count)
	if err := s.app.Save(rec); err != nil {
		slog.Warn("could not sync a file's reference count", "file", fileID, "error", err)
	}
}

// uploadPage is the upload screen.
type uploadPage struct {
	base
	Folders []formOption
	Folder  string
	Policy  storage.Policy
}

// referencesPage lists what points at a file.
type referencesPage struct {
	base
	File        *core.Record
	Attachments []*core.Record
	CanDetach   bool
}

// storageURL builds the download URL for a file.
func storageURL(id string) string {
	return "/storage/files/" + url.PathEscape(id) + "/download"
}

// openStorageDriver opens the configured driver.
//
// A failure is logged and storage degrades to unavailable rather than stopping
// the panel: an operator needs to reach the settings screen that would fix a bad
// storage path, so refusing to boot would remove the way to repair it.
func openStorageDriver(cfg *config.Config) storage.Driver {
	dir := cfg.StorageDir
	if dir == "" {
		dir = filepath.Join(cfg.DataDir, "storage")
	}

	driver, err := storage.NewLocalDriver(dir)
	if err != nil {
		slog.Error("the storage driver could not be opened; the file library is unavailable",
			"dir", dir, "error", err)
		return nil
	}
	return driver
}
