// Package repositories holds the PostgreSQL implementations of the read model's
// persistence operations, one repository per table group. A repository runs every
// statement on the *gorm.DB it was built with: built on a transaction, it is part of
// that transaction and never commits or opens one itself. The viewstore package
// composes them into the application ports.
package repositories
