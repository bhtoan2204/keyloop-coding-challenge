package repos

import (
	"context"
	"keyloop-coding-challenge/package/eventsourcing"

	"gorm.io/gorm"
)

type aggregateRepo struct {
	db        *gorm.DB
	serialize eventsourcing.Serializer
}

func newAggregateRepo(db *gorm.DB, s eventsourcing.Serializer) *aggregateRepo {
	return &aggregateRepo{
		serialize: s,
		db:        db,
	}
}

func (r *aggregateRepo) CreateIfNotExist(ctx context.Context, id, typ string) error {
	err := r.db.WithContext(ctx).Exec(`
		INSERT IGNORE INTO es_aggregate(id, version, aggregate_type)	
		VALUES(?, 0, ?)
	`, id, typ).Error
	if err != nil {
		return err
	}

	return nil
}

func (r *aggregateRepo) CheckAndUpdateVersion(ctx context.Context, agg eventsourcing.Aggregate) bool {
	root := agg.Root()

	aggregateId := root.AggregateID()
	expectedVersion := root.BaseVersion()
	newVersion := root.Version()

	query := r.db.WithContext(ctx).Exec(`
		UPDATE es_aggregate
		SET version = ?
		WHERE id = ?
			AND version = ?
	`, newVersion, aggregateId, expectedVersion)

	err := query.Error
	if err != nil {
		return false
	}

	return query.RowsAffected > 0
}

func (r *aggregateRepo) ReadSnapshot(ctx context.Context, aggregateID string, version int, agg eventsourcing.Aggregate) bool {
	result := struct {
		AggregateType    string `json:"aggregate_type"`
		AggregateVersion int    `json:"aggregate_version"`
		SnapshotVersion  int    `json:"snapshot_version"`
		Data             string `json:"data"`
	}{}

	err := r.db.WithContext(ctx).Raw(`
		SELECT a.aggregate_type, a.version as aggregate_version, eas.version as snapshot_version, eas.data
		FROM es_aggregate_snapshot eas
		JOIN es_aggregate a ON eas.aggregate_id = a.id
		WHERE eas.aggregate_id = ?
			AND eas.version >= ?
		ORDER BY eas.version DESC
		LIMIT 1
		`, aggregateID, version).Scan(&result).Error
	if err != nil {
		return false
	}

	if result.Data == "" {
		return false
	}

	root := agg.Root()

	err = r.serialize.Unmarshal([]byte(result.Data), agg)
	if err != nil {
		return false
	}

	root.SetInternal(aggregateID, result.SnapshotVersion, result.AggregateVersion)

	return true
}

func (r *aggregateRepo) CreateSnapshot(ctx context.Context, agg eventsourcing.Aggregate) error {
	root := agg.Root()
	aggregateId := root.AggregateID()
	version := root.Version()

	data, err := r.serialize.Marshal(agg)
	if err != nil {
		return err
	}

	err = r.db.WithContext(ctx).Exec(`
		INSERT INTO es_aggregate_snapshot (aggregate_id, version, data)
		VALUES(?, ?, ?)`, aggregateId, version, data).Error
	if err != nil {
		return err
	}

	return nil
}
