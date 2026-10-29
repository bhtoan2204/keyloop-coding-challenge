package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"keyloop-coding-challenge/internal/domain"
)

// ListInventory is a list request as received from the API.
type ListInventory struct {
	DealershipID string
	Make         string
	Model        string
	MinAgeDays   *int
	MaxAgeDays   *int
	AgingOnly    bool
	Status       string // IN_STOCK (default), SOLD or ALL
	Sort         string // age_desc (default) or age_asc
	Limit        int    // 0 means DefaultLimit
	Cursor       string
	AsOf         *domain.Date
}

type InventoryPage struct {
	AsOf       domain.Date
	Vehicles   []Vehicle
	Limit      int
	HasMore    bool
	NextCursor string
}

func (s *Service) ListInventory(ctx context.Context, q ListInventory) (*InventoryPage, error) {
	f, sortKey, err := validateList(q)
	if err != nil {
		return nil, err
	}
	fingerprint := listFingerprint(q, f, sortKey)

	pinned, after, err := resolveListCursor(q, sortKey, fingerprint)
	if err != nil {
		return nil, err
	}
	f.After = after
	businessDate, err := s.businessDate(ctx, q.DealershipID, pinned)
	if err != nil {
		return nil, err
	}
	f.BusinessDate = businessDate
	limit := f.Limit
	f.Limit = limit + 1 // one extra row tells whether another page exists

	rows, err := s.rm.ListVehicles(ctx, f)
	if err != nil {
		return nil, err
	}
	page := &InventoryPage{
		AsOf:  businessDate,
		Limit: limit,
	}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		page.HasMore = true
		page.NextCursor = encode(listCursor{
			Version:     1,
			Sort:        sortKey,
			AsOf:        businessDate.String(),
			Fingerprint: fingerprint,
			StockedOn:   last.StockedOn.String(),
			VehicleID:   last.VehicleID,
		})
	}
	page.Vehicles = rows
	return page, nil
}

func validateList(q ListInventory) (VehicleFilter, string, error) {
	f := VehicleFilter{DealershipID: q.DealershipID, AgingOnly: q.AgingOnly}
	var err error
	if f.Limit, err = validateLimit(q.Limit); err != nil {
		return f, "", err
	}
	switch strings.ToUpper(q.Status) {
	case "", string(domain.StatusInStock):
		f.Statuses = []domain.InventoryStatus{domain.StatusInStock}
	case string(domain.StatusSold):
		f.Statuses = []domain.InventoryStatus{domain.StatusSold}
	case StatusAll:
	default:
		return f, "", domain.Invalid("status", "must be IN_STOCK, SOLD or ALL")
	}
	sortKey := q.Sort
	switch sortKey {
	case "":
		sortKey = SortAgeDesc
	case SortAgeDesc:
	case SortAgeAsc:
		f.Descending = true
	default:
		return f, "", domain.Invalid("sort", "must be age_desc or age_asc")
	}
	for name, v := range map[string]*string{"make": &q.Make, "model": &q.Model} {
		*v = strings.TrimSpace(*v)
		if utf8.RuneCountInString(*v) > 64 {
			return f, "", domain.Invalid(name, "must be at most 64 characters")
		}
	}
	f.Make, f.Model = q.Make, q.Model
	for name, v := range map[string]*int{"min_age_days": q.MinAgeDays, "max_age_days": q.MaxAgeDays} {
		if v != nil && (*v < 0 || *v > 100000) {
			return f, "", domain.Invalid(name, "must be between 0 and 100000")
		}
	}
	if q.MinAgeDays != nil && q.MaxAgeDays != nil && *q.MinAgeDays > *q.MaxAgeDays {
		return f, "", domain.Invalid("min_age_days", "must not exceed max_age_days")
	}
	f.MinAgeDays, f.MaxAgeDays = q.MinAgeDays, q.MaxAgeDays
	return f, sortKey, nil
}

// listFingerprint identifies the filters and sort a cursor belongs to (not the limit,
// which may change between pages).
func listFingerprint(q ListInventory, f VehicleFilter, sortKey string) string {
	optional := func(v *int) string {
		if v == nil {
			return "-"
		}
		return fmt.Sprint(*v)
	}
	raw := strings.Join([]string{
		q.DealershipID, strings.ToLower(f.Make), strings.ToLower(f.Model),
		optional(f.MinAgeDays), optional(f.MaxAgeDays), fmt.Sprint(f.AgingOnly),
		fmt.Sprint(f.Statuses), sortKey,
	}, "\x1f")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}
