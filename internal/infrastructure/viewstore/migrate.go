package viewstore

import (
	"gorm.io/gorm"

	"keyloop-coding-challenge/internal/infrastructure/viewstore/migrations"
)

// Migrate applies the read_model migrations (see migrations.Migrate).
func Migrate(db *gorm.DB) error {
	return migrations.Migrate(db)
}
