package repository

import (
	"context"
	"crypto/md5"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/mysql"

	"selfcloud/internal/db"
	"selfcloud/internal/domain"
	"selfcloud/internal/pathutil"
)

type nodeRepo struct {
	db db.DBTX
}

func NewNodeRepository(d db.DBTX) NodeRepository {
	return &nodeRepo{db: d}
}

func (r *nodeRepo) GetByPath(ctx context.Context, userID int64, path string) (*domain.Node, error) {
	path = normalizePath(path)
	query, args, err := dialect.From("nodes").
		Select("id", "user_id", "path", "parent_path", "name", "is_dir", "size", "etag", "file_id", "mtime", "content_type", "created_at", "updated_at").
		Where(goqu.Ex{"user_id": userID, "path": path}).
		Limit(1).
		ToSQL()

	if err != nil {
		return nil, fmt.Errorf("failed to build SQL for GetByPath: %w", err)
	}

	node := &domain.Node{}
	var isDirInt int

	row := r.db.QueryRowContext(ctx, query, args...)
	err = row.Scan(&node.ID, &node.UserID, &node.Path, &node.ParentPath, &node.Name, &isDirInt, &node.Size, &node.ETag, &node.FileID, &node.MTime, &node.ContentType, &node.CreatedAt, &node.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query node at path %s: %w", path, err)
	}

	node.IsDir = isDirInt == 1
	return node, nil
}

func (r *nodeRepo) ListByParentPath(ctx context.Context, userID int64, parentPath string) ([]*domain.Node, error) {
	parentPath = normalizePath(parentPath)
	query, args, err := dialect.From("nodes").
		Select("id", "user_id", "path", "parent_path", "name", "is_dir", "size", "etag", "file_id", "mtime", "content_type", "created_at", "updated_at").
		Where(goqu.Ex{"user_id": userID, "parent_path": parentPath}).
		Order(goqu.I("is_dir").Desc(), goqu.I("name").Asc()).
		ToSQL()

	if err != nil {
		return nil, fmt.Errorf("failed to build SQL for ListByParentPath: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query child nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*domain.Node
	for rows.Next() {
		node := &domain.Node{}
		var isDirInt int

		if err := rows.Scan(&node.ID, &node.UserID, &node.Path, &node.ParentPath, &node.Name, &isDirInt, &node.Size, &node.ETag, &node.FileID, &node.MTime, &node.ContentType, &node.CreatedAt, &node.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan child node: %w", err)
		}

		node.IsDir = isDirInt == 1
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during child nodes iteration: %w", err)
	}

	return nodes, nil
}

func (r *nodeRepo) Create(ctx context.Context, node *domain.Node) error {
	node.Path = normalizePath(node.Path)
	node.ParentPath = normalizePath(node.ParentPath)
	isDirInt := 0
	if node.IsDir {
		isDirInt = 1
	}

	now := time.Now()
	if node.MTime.IsZero() {
		node.MTime = now
	}

	query, args, err := dialect.Insert("nodes").
		Cols("user_id", "path", "parent_path", "name", "is_dir", "size", "etag", "file_id", "mtime", "content_type", "created_at", "updated_at").
		Vals(goqu.Vals{node.UserID, node.Path, node.ParentPath, node.Name, isDirInt, node.Size, node.ETag, node.FileID, node.MTime, node.ContentType, now, now}).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for Create node: %w", err)
	}

	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to insert node: %w", err)
	}

	id, err := res.LastInsertId()
	if err == nil {
		node.ID = id
	}

	return nil
}

func (r *nodeRepo) Update(ctx context.Context, node *domain.Node) error {
	node.Path = normalizePath(node.Path)
	node.ParentPath = normalizePath(node.ParentPath)
	isDirInt := 0
	if node.IsDir {
		isDirInt = 1
	}

	query, args, err := dialect.Update("nodes").
		Set(goqu.Record{
			"name":         node.Name,
			"parent_path":  node.ParentPath,
			"size":         node.Size,
			"etag":         node.ETag,
			"mtime":        node.MTime,
			"content_type": node.ContentType,
			"is_dir":       isDirInt,
			"updated_at":   time.Now(),
		}).
		Where(goqu.Ex{"user_id": node.UserID, "path": node.Path}).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for Update node: %w", err)
	}

	_, err = r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update node: %w", err)
	}

	return nil
}

func (r *nodeRepo) Delete(ctx context.Context, userID int64, path string) error {
	path = normalizePath(path)
	query, args, err := dialect.Delete("nodes").
		Where(goqu.Ex{"user_id": userID, "path": path}).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for Delete node: %w", err)
	}

	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to execute Delete node: %w", err)
	}
	return nil
}

func (r *nodeRepo) DeletePrefix(ctx context.Context, userID int64, pathPrefix string) error {
	pathPrefix = normalizePath(pathPrefix)
	if !strings.HasSuffix(pathPrefix, "/") {
		pathPrefix += "/"
	}

	escapedPrefix := escapeSQLLike(pathPrefix)

	query, args, err := dialect.Delete("nodes").
		Where(goqu.Ex{"user_id": userID}, goqu.Or(
			goqu.Ex{"path": strings.TrimSuffix(pathPrefix, "/")},
			goqu.L("path LIKE ?", escapedPrefix+"%"),
		)).
		ToSQL()

	if err != nil {
		return fmt.Errorf("failed to build SQL for DeletePrefix: %w", err)
	}

	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to execute DeletePrefix: %w", err)
	}
	return nil
}

func (r *nodeRepo) MovePrefix(ctx context.Context, userID int64, oldPrefix, newPrefix string) error {
	oldPrefix = normalizePath(oldPrefix)
	newPrefix = normalizePath(newPrefix)
	if oldPrefix == newPrefix {
		return nil
	}

	targetParent, targetName := pathutil.SplitPath(newPrefix)
	now := time.Now()

	// 1. Update the moved root node itself
	updateRootQuery, rootArgs, err := dialect.Update("nodes").
		Set(goqu.Record{
			"path":        newPrefix,
			"parent_path": targetParent,
			"name":        targetName,
			"updated_at":  now,
		}).
		Where(goqu.Ex{"user_id": userID, "path": oldPrefix}).
		ToSQL()
	if err != nil {
		return fmt.Errorf("failed to build SQL for MovePrefix root update: %w", err)
	}

	if _, err := r.db.ExecContext(ctx, updateRootQuery, rootArgs...); err != nil {
		return fmt.Errorf("failed to update moved root node: %w", err)
	}

	// 2. Update all descendant nodes in a single set-based query
	pattern := oldPrefix
	if !strings.HasSuffix(pattern, "/") {
		pattern += "/"
	}
	escapedPattern := escapeSQLLike(pattern)
	subIndex := len(oldPrefix) + 1 // 1-based index in SQL SUBSTRING

	descendantQuery := `UPDATE nodes SET 
		path = CONCAT(?, SUBSTRING(path, ?)),
		parent_path = CONCAT(?, SUBSTRING(parent_path, ?)),
		updated_at = ?
		WHERE user_id = ? AND path LIKE ?`

	if _, err := r.db.ExecContext(ctx, descendantQuery, newPrefix, subIndex, newPrefix, subIndex, now, userID, escapedPattern+"%"); err != nil {
		return fmt.Errorf("failed to update moved descendants: %w", err)
	}

	return nil
}

const maxParentDirRecursionDepth = 64

func (r *nodeRepo) EnsureParentDirectoryExists(ctx context.Context, userID int64, parentPath string) error {
	return r.ensureParentDirectoryExistsWithDepth(ctx, userID, parentPath, 0)
}

func (r *nodeRepo) ensureParentDirectoryExistsWithDepth(ctx context.Context, userID int64, parentPath string, depth int) error {
	if depth > maxParentDirRecursionDepth {
		return fmt.Errorf("exceeded maximum directory depth (%d) for %s", maxParentDirRecursionDepth, parentPath)
	}
	if parentPath == "" || parentPath == "/" {
		return nil
	}
	existing, err := r.GetByPath(ctx, userID, parentPath)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}

	grandParent, name := pathutil.SplitPath(parentPath)
	if err := r.ensureParentDirectoryExistsWithDepth(ctx, userID, grandParent, depth+1); err != nil {
		return err
	}

	fileID := pathutil.GenerateFileID(userID)
	etag := fmt.Sprintf("%x", md5.Sum([]byte(parentPath+fileID)))
	node := &domain.Node{
		UserID:      userID,
		Path:        parentPath,
		ParentPath:  grandParent,
		Name:        name,
		IsDir:       true,
		Size:        0,
		ETag:        etag,
		FileID:      fileID,
		MTime:       time.Now(),
		ContentType: "httpd/unix-directory",
	}

	if err := r.Create(ctx, node); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(err.Error(), "1062") {
			return nil
		}
		return err
	}
	return nil
}

func normalizePath(p string) string {
	return pathutil.NormalizePath(p)
}

func escapeSQLLike(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '%', '_':
			b.WriteRune('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
