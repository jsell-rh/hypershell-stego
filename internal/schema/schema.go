// Package schema initializes a fresh Hypershell installation.
package schema

import (
	_ "embed"

	"github.com/jsell-rh/hypershell-stego/out/outbox"
	"github.com/jsell-rh/hypershell-stego/out/storage"
	"gorm.io/gorm"
)

//go:embed bootstrap.sql
var bootstrap string

func init() {
	storage.Register("003_hypershell", func(db *gorm.DB) error {
		if err := db.Exec(outbox.SchemaSQL).Error; err != nil {
			return err
		}
		return db.Exec(bootstrap).Error
	})
}

// Initialize runs all schema work under STEGO's generation lock and transaction.
// It rejects old or unknown installations before any write.
func Initialize(db *gorm.DB) error { return storage.Migrate(db) }
