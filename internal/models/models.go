package models

import "time"

// User represents an authenticated user
type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"` // admin, user
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UserPublic represents a user without sensitive data
type UserPublic struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

// PasswordReset represents a password reset token
type PasswordReset struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
	CreatedAt time.Time `json:"created_at"`
}

// TableInfo represents metadata about a database table
type TableInfo struct {
	Name       string       `json:"name"`
	Schema     string       `json:"schema"`
	RowCount   int64        `json:"row_count"`
	Columns    []ColumnInfo `json:"columns"`
	PrimaryKey string       `json:"primary_key"`
	CreatedAt  *time.Time   `json:"created_at,omitempty"`
}

// ColumnInfo represents metadata about a table column
type ColumnInfo struct {
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Nullable     bool    `json:"nullable"`
	DefaultValue *string `json:"default_value,omitempty"`
	IsPrimaryKey bool    `json:"is_primary_key"`
	IsForeignKey bool    `json:"is_foreign_key"`
	References   *string `json:"references,omitempty"`
}

// QueryRequest represents a SQL query request
type QueryRequest struct {
	SQL    string        `json:"sql"`
	Params []interface{} `json:"params,omitempty"`
}

// QueryResult represents the result of a SQL query
type QueryResult struct {
	Columns      []string                 `json:"columns"`
	Rows         []map[string]interface{} `json:"rows"`
	RowsAffected int64                    `json:"rows_affected"`
	Duration     string                   `json:"duration"`
}

// CreateTableRequest represents a request to create a new table
type CreateTableRequest struct {
	Name    string             `json:"name"`
	Columns []CreateColumnSpec `json:"columns"`
}

// CreateColumnSpec represents a column specification for table creation
type CreateColumnSpec struct {
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Nullable     bool    `json:"nullable"`
	DefaultValue *string `json:"default_value,omitempty"`
	IsPrimaryKey bool    `json:"is_primary_key"`
	IsUnique     bool    `json:"is_unique"`
}

// ImportRequest represents a data import request
type ImportRequest struct {
	TableName string                   `json:"table_name,omitempty"`
	Data      []map[string]interface{} `json:"data,omitempty"`
	SQL       string                   `json:"sql,omitempty"`
}

// PaginationParams represents pagination parameters
type PaginationParams struct {
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
	OrderBy  string            `json:"order_by"`
	Order    string            `json:"order"` // asc, desc
	Filters  []FilterCondition `json:"filters,omitempty"`
	Select   string            `json:"select,omitempty"` // comma-separated column names
}

// FilterCondition represents a single filter condition
// Operators: eq, neq, gt, gte, lt, lte, like, ilike, is, in
type FilterCondition struct {
	Column   string      `json:"column"`
	Operator string      `json:"operator"`
	Value    interface{} `json:"value"`
}

// PaginatedResponse represents a paginated response
type PaginatedResponse struct {
	Data       interface{} `json:"data"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	TotalRows  int64       `json:"total_rows"`
	TotalPages int         `json:"total_pages"`
}

// AuthRequest represents login/register request
type AuthRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// AuthResponse represents authentication response
type AuthResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	User         *User  `json:"user"`
}

// ForgotPasswordRequest represents forgot password request
type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

// ResetPasswordRequest represents reset password request
type ResetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}
