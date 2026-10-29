package referencedata_test

import (
	"context"
	"errors"
	"testing"

	"keyloop-coding-challenge/internal/application/command"
	"keyloop-coding-challenge/internal/infrastructure/eventstore/repos"
	"keyloop-coding-challenge/internal/infrastructure/referencedata"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDealershipLocation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := repos.Migrate(db); err != nil {
		t.Fatal(err)
	}
	dir := referencedata.NewDealerships(db)
	ctx := context.Background()

	for id, zone := range map[string]string{"d-001": "Europe/London", "d-002": "Australia/Sydney"} {
		loc, err := dir.Location(ctx, id)
		if err != nil || loc.String() != zone {
			t.Errorf("%s: got %v, %v; want %s", id, loc, err, zone)
		}
	}
	if _, err := dir.Location(ctx, "d-404"); !errors.Is(err, command.ErrDealershipNotFound) {
		t.Errorf("unknown dealership: %v", err)
	}
	if err := db.Exec(`INSERT INTO dealership VALUES ('d-bad', 'Bad', 'Mars/Olympus')`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := dir.Location(ctx, "d-bad"); err == nil || errors.Is(err, command.ErrDealershipNotFound) {
		t.Errorf("invalid zone: %v", err)
	}
}
