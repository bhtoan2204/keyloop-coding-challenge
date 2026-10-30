package viewstore

import (
	"context"
	"fmt"

	"keyloop-coding-challenge/internal/application/query"
)

func (p *Postgres) GetVehicle(ctx context.Context, dealershipID, vehicleID string) (*query.Vehicle, error) {
	rows, err := p.db.WithContext(ctx).Raw(`SELECT `+vehicleColumns+` FROM read_model.inventory_vehicle WHERE vehicle_id = ? AND dealership_id = ?`,
		vehicleID, dealershipID).Rows()
	if err != nil {
		return nil, fmt.Errorf("get vehicle: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", query.ErrVehicleNotFound, vehicleID)
	}
	r, err := scanVehicle(rows)
	if err != nil {
		return nil, err
	}
	v := toQueryVehicle(r)
	return &v, nil
}
