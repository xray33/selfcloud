package domain

import (
	"errors"
	"time"
)

var ErrAlreadyExists = errors.New("node already exists")

type Node struct {
	ID          int64     `db:"id" json:"id"`
	UserID      int64     `db:"user_id" json:"user_id"`
	Path        string    `db:"path" json:"path"`
	ParentPath  string    `db:"parent_path" json:"parent_path"`
	Name        string    `db:"name" json:"name"`
	IsDir       bool      `db:"is_dir" json:"is_dir"`
	Size        int64     `db:"size" json:"size"`
	ETag        string    `db:"etag" json:"etag"`
	FileID      string    `db:"file_id" json:"file_id"`
	MTime       time.Time `db:"mtime" json:"mtime"`
	ContentType string    `db:"content_type" json:"content_type"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
}
