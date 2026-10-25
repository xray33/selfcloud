// Package indexer provides filesystem-to-database reconciliation.
// It walks the storage directory for one or all users and syncs file metadata
// into the nodes table, creating missing entries, updating stale ones, and
// optionally removing DB rows whose files no longer exist on disk.
package indexer

import (
	"context"
	"crypto/md5"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"selfcloud/internal/domain"
	"selfcloud/internal/pathutil"
	"selfcloud/internal/repository"
)

// Stats holds counters from a completed reindex run.
type Stats struct {
	Created int
	Updated int
	Deleted int
	Errors  int
}

// Reindexer reconciles the filesystem with the database for a given user.
type Reindexer struct {
	nodeRepo   repository.NodeRepository
	storageDir string
	logger     *slog.Logger
	dryRun     bool
}

// New creates a Reindexer.
func New(nodeRepo repository.NodeRepository, storageDir string, logger *slog.Logger, dryRun bool) *Reindexer {
	return &Reindexer{
		nodeRepo:   nodeRepo,
		storageDir: storageDir,
		logger:     logger,
		dryRun:     dryRun,
	}
}

// ReindexUser walks the storage directory for a user and syncs nodes into the DB.
// If removeOrphans is true, DB entries with no corresponding file are deleted.
func (r *Reindexer) ReindexUser(ctx context.Context, user *domain.User, removeOrphans bool) (Stats, error) {
	var stats Stats

	userDir := filepath.Join(r.storageDir, user.Username)
	if _, err := os.Stat(userDir); os.IsNotExist(err) {
		r.logger.Info("Storage directory does not exist for user, skipping",
			slog.String("username", user.Username),
			slog.String("dir", userDir),
		)
		return stats, nil
	}

	diskPaths := make(map[string]struct{})
	diskPaths["/"] = struct{}{} // root always exists

	err := filepath.WalkDir(userDir, func(absPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			r.logger.Warn("Walk error, skipping entry",
				slog.String("path", absPath),
				slog.Any("error", walkErr),
			)
			stats.Errors++
			return nil
		}

		meta, err := inspectFSEntry(userDir, absPath, d)
		if err != nil {
			r.logger.Warn("Cannot stat entry", slog.String("path", absPath), slog.Any("error", err))
			stats.Errors++
			return nil
		}
		if meta == nil {
			return nil
		}

		diskPaths[meta.relPath] = struct{}{}
		r.syncNode(ctx, user, meta, &stats)
		return nil
	})

	if err != nil {
		return stats, fmt.Errorf("walk failed for user %s: %w", user.Username, err)
	}

	if err := r.ensureRootNode(ctx, user); err != nil {
		r.logger.Warn("Could not ensure root node", slog.Any("error", err))
	}

	if removeOrphans {
		orphanStats, err := r.removeOrphans(ctx, user, diskPaths)
		if err != nil {
			r.logger.Error("Orphan removal failed", slog.Any("error", err))
		}
		stats.Deleted += orphanStats
	}

	if !r.dryRun && (stats.Created > 0 || stats.Updated > 0 || stats.Deleted > 0) {
		_ = r.nodeRepo.TouchAncestors(ctx, user.ID, "/")
		_ = r.nodeRepo.RecalculateDirectorySizes(ctx, user.ID)
	}

	return stats, nil
}

type fsEntryMeta struct {
	relPath     string
	parentPath  string
	name        string
	isDir       bool
	size        int64
	mtime       time.Time
	contentType string
	etag        string
}

func inspectFSEntry(userDir, absPath string, d fs.DirEntry) (*fsEntryMeta, error) {
	rel, err := filepath.Rel(userDir, absPath)
	if err != nil || rel == "." {
		return nil, nil // skip root itself
	}
	info, err := d.Info()
	if err != nil {
		return nil, err
	}

	relPath := "/" + filepath.ToSlash(rel)
	isDir := d.IsDir()
	size := int64(0)
	contentType := "application/octet-stream"
	if isDir {
		contentType = "httpd/unix-directory"
	} else {
		size = info.Size()
	}
	mtime := info.ModTime()
	parentPath, name := pathutil.SplitPath(relPath)

	return &fsEntryMeta{
		relPath:     relPath,
		parentPath:  parentPath,
		name:        name,
		isDir:       isDir,
		size:        size,
		mtime:       mtime,
		contentType: contentType,
		etag:        etagFromFS(relPath, size, mtime),
	}, nil
}

func (r *Reindexer) syncNode(ctx context.Context, user *domain.User, meta *fsEntryMeta, stats *Stats) {
	existing, dbErr := r.nodeRepo.GetByPath(ctx, user.ID, meta.relPath)
	if dbErr != nil {
		r.logger.Error("DB lookup failed", slog.String("path", meta.relPath), slog.Any("error", dbErr))
		stats.Errors++
		return
	}

	if existing == nil {
		if err := r.createMissingNode(ctx, user, meta); err != nil {
			r.logger.Error("Failed to create node", slog.String("path", meta.relPath), slog.Any("error", err))
			stats.Errors++
			return
		}
		stats.Created++
		return
	}

	updated, err := r.updateStaleNode(ctx, existing, meta)
	if err != nil {
		r.logger.Error("Failed to update node", slog.String("path", meta.relPath), slog.Any("error", err))
		stats.Errors++
		return
	}
	if updated {
		stats.Updated++
	}
}

func (r *Reindexer) createMissingNode(ctx context.Context, user *domain.User, meta *fsEntryMeta) error {
	r.logger.Info("Creating missing node",
		slog.String("path", meta.relPath),
		slog.Bool("dry_run", r.dryRun),
	)
	if r.dryRun {
		return nil
	}
	node := &domain.Node{
		UserID:      user.ID,
		Path:        meta.relPath,
		ParentPath:  meta.parentPath,
		Name:        meta.name,
		IsDir:       meta.isDir,
		Size:        meta.size,
		ETag:        meta.etag,
		FileID:      pathutil.GenerateFileID(user.ID),
		MTime:       meta.mtime,
		ContentType: meta.contentType,
	}
	return r.nodeRepo.Create(ctx, node)
}

func (r *Reindexer) updateStaleNode(ctx context.Context, existing *domain.Node, meta *fsEntryMeta) (bool, error) {
	changed := existing.Size != meta.size ||
		existing.MTime.Unix() != meta.mtime.Unix() ||
		existing.IsDir != meta.isDir ||
		existing.Name != meta.name ||
		existing.ParentPath != meta.parentPath

	if !changed {
		return false, nil
	}

	r.logger.Info("Updating stale node",
		slog.String("path", meta.relPath),
		slog.Bool("dry_run", r.dryRun),
	)
	if r.dryRun {
		return true, nil
	}

	existing.Name = meta.name
	existing.ParentPath = meta.parentPath
	existing.Size = meta.size
	existing.MTime = meta.mtime
	existing.ETag = meta.etag
	existing.ContentType = meta.contentType
	return true, r.nodeRepo.Update(ctx, existing)
}

func (r *Reindexer) ensureRootNode(ctx context.Context, user *domain.User) error {
	existing, err := r.nodeRepo.GetByPath(ctx, user.ID, "/")
	if err != nil || existing != nil {
		return err
	}
	if r.dryRun {
		r.logger.Info("Would create root node for user", slog.String("username", user.Username))
		return nil
	}
	node := &domain.Node{
		UserID:      user.ID,
		Path:        "/",
		ParentPath:  "",
		Name:        "",
		IsDir:       true,
		ETag:        fmt.Sprintf("root_%s", user.Username),
		FileID:      fmt.Sprintf("%08doc_root", user.ID),
		MTime:       time.Now(),
		ContentType: "httpd/unix-directory",
	}
	return r.nodeRepo.Create(ctx, node)
}

type orphanPruner struct {
	ctx       context.Context
	user      *domain.User
	diskPaths map[string]struct{}
	r         *Reindexer
	visited   map[string]bool
	count     int
}

func (p *orphanPruner) processChild(child *domain.Node) error {
	if _, onDisk := p.diskPaths[child.Path]; !onDisk {
		p.r.deleteOrphanNode(p.ctx, p.user.ID, child.Path)
		p.count++
		return nil
	}
	if child.IsDir {
		return p.walk(child.Path)
	}
	return nil
}

func (p *orphanPruner) walk(parentPath string) error {
	if p.visited[parentPath] {
		return nil
	}
	p.visited[parentPath] = true

	children, err := p.r.nodeRepo.ListByParentPath(p.ctx, p.user.ID, parentPath)
	if err != nil {
		return err
	}
	for _, child := range children {
		if child.Path == parentPath || child.Path == "/" || child.Name == "" || p.visited[child.Path] {
			continue
		}
		if err := p.processChild(child); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reindexer) deleteOrphanNode(ctx context.Context, userID int64, childPath string) {
	r.logger.Info("Removing orphan DB node",
		slog.String("path", childPath),
		slog.Bool("dry_run", r.dryRun),
	)
	if r.dryRun {
		return
	}
	if err := r.nodeRepo.Delete(ctx, userID, childPath); err != nil {
		r.logger.Error("Failed to delete orphan node",
			slog.String("path", childPath),
			slog.Any("error", err),
		)
	}
}

func (r *Reindexer) removeOrphans(ctx context.Context, user *domain.User, diskPaths map[string]struct{}) (int, error) {
	pruner := &orphanPruner{
		ctx:       ctx,
		user:      user,
		diskPaths: diskPaths,
		r:         r,
		visited:   make(map[string]bool),
	}
	err := pruner.walk("/")
	return pruner.count, err
}

// etagFromFS computes an ETag from file path, size, and mtime — no file reads needed.
func etagFromFS(relPath string, size int64, mtime time.Time) string {
	key := fmt.Sprintf("%s:%d:%d", relPath, size, mtime.Unix())
	return fmt.Sprintf("%x", md5.Sum([]byte(key)))
}
