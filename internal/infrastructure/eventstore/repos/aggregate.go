package repos

import (
	"context"
	"fmt"

	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/gorm"
)

type aggregateRepo struct {
	db        *gorm.DB
	serialize eventsourcing.Serializer
}

func newAggregateRepo(db *gorm.DB, s eventsourcing.Serializer) *aggregateRepo {
	return &aggregateRepo{serialize: s, db: db}
}

func (r *aggregateRepo) CreateIfNotExist(ctx context.Context, id, typ string) error {
	if err := r.insertAggregate(ctx, id, typ); err != nil {
		return err
	}
	_, existingType, found, err := r.aggregateByID(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("aggregate %s was not created", id)
	}
	if existingType != typ {
		return fmt.Errorf("%w: aggregate %s has type %s", ErrAggregateTypeMismatch, id, existingType)
	}
	return nil
}

func (r *aggregateRepo) insertAggregate(ctx context.Context, id, typ string) error {
	stmt := `INSERT INTO es_aggregate(id, version, aggregate_type) VALUES(?, 0, ?) ON CONFLICT(id) DO NOTHING`
	if r.db.Dialector.Name() == "mysql" {
		stmt = `INSERT INTO es_aggregate(id, version, aggregate_type) VALUES(?, 0, ?) ON DUPLICATE KEY UPDATE id = id`
	}
	return r.db.WithContext(ctx).Exec(stmt, id, typ).Error
}

func (r *aggregateRepo) FindAggregate(ctx context.Context, id string) (int, string, error) {
	version, typ, found, err := r.aggregateByID(ctx, id)
	if err != nil {
		return 0, "", err
	}
	if !found {
		return 0, "", ErrAggregateNotFound
	}
	return version, typ, nil
}

func (r *aggregateRepo) aggregateByID(ctx context.Context, id string) (int, string, bool, error) {
	rows, err := r.db.WithContext(ctx).Raw(
		`SELECT version, aggregate_type FROM es_aggregate WHERE id = ?`, id,
	).Rows()
	if err != nil {
		return 0, "", false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, "", false, rows.Err()
	}
	var version int
	var typ string
	if err := rows.Scan(&version, &typ); err != nil {
		return 0, "", false, err
	}
	return version, typ, true, rows.Err()
}

func (r *aggregateRepo) CheckAndUpdateVersion(ctx context.Context, agg eventsourcing.Aggregate) error {
	root := agg.Root()
	aggregateID := root.AggregateID()
	expectedVersion := root.BaseVersion()
	newVersion := root.Version()

	query := r.db.WithContext(ctx).Exec(`
		UPDATE es_aggregate
		SET version = ?
		WHERE id = ? AND version = ?
	`, newVersion, aggregateID, expectedVersion)
	if query.Error != nil {
		return query.Error
	}
	if query.RowsAffected == 0 {
		return fmt.Errorf("%w: aggregateID=%s expectedVersion=%d newVersion=%d",
			ErrConcurrencyConflict, aggregateID, expectedVersion, newVersion)
	}
	return nil
}

func (r *aggregateRepo) ReadSnapshot(ctx context.Context, aggregateID string, agg eventsourcing.Aggregate) (bool, error) {
	rows, err := r.db.WithContext(ctx).Raw(`
		SELECT a.version, eas.version, eas.data
		FROM es_aggregate_snapshot eas
		JOIN es_aggregate a ON eas.aggregate_id = a.id
		WHERE eas.aggregate_id = ?
			AND eas.version <= a.version
		ORDER BY eas.version DESC
		LIMIT 1
	`, aggregateID).Rows()
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return false, rows.Err()
	}
	var (
		aggregateVersion int
		snapshotVersion  int
		data             string
	)
	if err := rows.Scan(&aggregateVersion, &snapshotVersion, &data); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if data == "" {
		return false, fmt.Errorf("snapshot for aggregate %s at version %d is empty", aggregateID, snapshotVersion)
	}
	if err := r.serialize.Unmarshal([]byte(data), agg); err != nil {
		return false, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	agg.Root().SetInternal(aggregateID, snapshotVersion, aggregateVersion)
	return true, nil
}

func (r *aggregateRepo) CreateSnapshot(ctx context.Context, agg eventsourcing.Aggregate) error {
	root := agg.Root()
	data, err := r.serialize.Marshal(agg)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	err = r.db.WithContext(ctx).Exec(`
		INSERT INTO es_aggregate_snapshot (aggregate_id, version, data)
		VALUES(?, ?, ?)`, root.AggregateID(), root.Version(), data).Error
	if err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}
	return nil
}
