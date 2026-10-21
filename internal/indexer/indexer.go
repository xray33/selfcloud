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

	// Track all relative paths found on disk for orphan detection.
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

		// Compute the relative WebDAV path.
		rel, err := filepath.Rel(userDir, absPath)
		if err != nil || rel == "." {
			return nil // skip the root itself; we handle it separately
		}
		relPath := "/" + filepath.ToSlash(rel)
		diskPaths[relPath] = struct{}{}

		info, err := d.Info()
		if err != nil {
			r.logger.Warn("Cannot stat entry", slog.String("path", absPath), slog.Any("error", err))
			stats.Errors++
			return nil
		}

		// Derive metadata from filesystem.
		isDir := d.IsDir()
		size := int64(0)
		if !isDir {
			size = info.Size()
		}
		mtime := info.ModTime()
		contentType := "application/octet-stream"
		if isDir {
			contentType = "httpd/unix-directory"
		}
		etag := etagFromFS(relPath, size, mtime)
		parentPath, name := pathutil.SplitPath(relPath)

		// Check existing DB node.
		existing, dbErr := r.nodeRepo.GetByPath(ctx, user.ID, relPath)
		if dbErr != nil {
			r.logger.Error("DB lookup failed",
				slog.String("path", relPath),
				slog.Any("error", dbErr),
			)
			stats.Errors++
			return nil
		}

		if existing == nil {
			// Node missing in DB → create it.
			node := &domain.Node{
				UserID:      user.ID,
				Path:        relPath,
				ParentPath:  parentPath,
				Name:        name,
				IsDir:       isDir,
				Size:        size,
				ETag:        etag,
				FileID:      pathutil.GenerateFileID(user.ID),
				MTime:       mtime,
				ContentType: contentType,
			}
			r.logger.Info("Creating missing node",
				slog.String("path", relPath),
				slog.Bool("dry_run", r.dryRun),
			)
			if !r.dryRun {
				if err := r.nodeRepo.Create(ctx, node); err != nil {
					r.logger.Error("Failed to create node", slog.String("path", relPath), slog.Any("error", err))
					stats.Errors++
					return nil
				}
			}
			stats.Created++
		} else {
			// Node exists — check if metadata drifted.
			changed := existing.Size != size ||
				existing.MTime.Unix() != mtime.Unix() ||
				existing.IsDir != isDir ||
				existing.Name != name ||
				existing.ParentPath != parentPath

			if changed {
				r.logger.Info("Updating stale node",
					slog.String("path", relPath),
					slog.Bool("dry_run", r.dryRun),
				)
				if !r.dryRun {
					existing.Name = name
					existing.ParentPath = parentPath
					existing.Size = size
					existing.MTime = mtime
					existing.ETag = etag
					existing.ContentType = contentType
					if err := r.nodeRepo.Update(ctx, existing); err != nil {
						r.logger.Error("Failed to update node", slog.String("path", relPath), slog.Any("error", err))
						stats.Errors++
						return nil
					}
				}
				stats.Updated++
			}
		}

		return nil
	})

	if err != nil {
		return stats, fmt.Errorf("walk failed for user %s: %w", user.Username, err)
	}

	// Ensure root node exists.
	if err := r.ensureRootNode(ctx, user); err != nil {
		r.logger.Warn("Could not ensure root node", slog.Any("error", err))
	}

	// Orphan removal: DB nodes with no corresponding disk file.
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

func (r *Reindexer) removeOrphans(ctx context.Context, user *domain.User, diskPaths map[string]struct{}) (int, error) {
	count := 0
	visited := make(map[string]bool)

	var walk func(parentPath string) error
	walk = func(parentPath string) error {
		if visited[parentPath] {
			return nil
		}
		visited[parentPath] = true

		children, err := r.nodeRepo.ListByParentPath(ctx, user.ID, parentPath)
		if err != nil {
			return err
		}
		for _, child := range children {
			if child.Path == parentPath || child.Path == "/" || child.Name == "" || visited[child.Path] {
				continue
			}

			if _, onDisk := diskPaths[child.Path]; !onDisk {
				r.logger.Info("Removing orphan DB node",
					slog.String("path", child.Path),
					slog.Bool("dry_run", r.dryRun),
				)
				if !r.dryRun {
					if err := r.nodeRepo.Delete(ctx, user.ID, child.Path); err != nil {
						r.logger.Error("Failed to delete orphan node",
							slog.String("path", child.Path),
							slog.Any("error", err),
						)
					}
				}
				count++
			} else if child.IsDir {
				// Recurse into existing directories.
				if err := walk(child.Path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return count, walk("/")
}

// etagFromFS computes an ETag from file path, size, and mtime — no file reads needed.
func etagFromFS(relPath string, size int64, mtime time.Time) string {
	key := fmt.Sprintf("%s:%d:%d", relPath, size, mtime.Unix())
	return fmt.Sprintf("%x", md5.Sum([]byte(key)))
}
