package query

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"keyloop-coding-challenge/internal/domain"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100

	SortAgeDesc = "age_desc" // oldest stock first (default)
	SortAgeAsc  = "age_asc"

	StatusAll = "ALL"
)

type Service struct {
	rm    ReadModel
	clock Clock
}

func NewService(rm ReadModel, clock Clock) *Service {
	return &Service{rm: rm, clock: clock}
}

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

type VehicleResult struct {
	Vehicle Vehicle
	AsOf    domain.Date
}

type HistoryPage struct {
	Entries    []HistoryEntry
	Limit      int
	HasMore    bool
	NextCursor string
}

type SummaryResult struct {
	Summary
	AsOf domain.Date
}

// listCursor is the opaque keyset cursor. It pins the business date and records a
// fingerprint of the filters and sort, so a page cannot be continued with different
// parameters or across a dealership-local midnight. It is not signed: it only selects
// rows the caller could request directly anyway.
type listCursor struct {
	Version     int    `json:"v"`
	Sort        string `json:"s"`
	AsOf        string `json:"a"`
	Fingerprint string `json:"f"`
	StockedOn   string `json:"k1"`
	VehicleID   string `json:"k2"`
}

func (s *Service) ListInventory(ctx context.Context, q ListInventory) (*InventoryPage, error) {
	f, sortKey, err := validateList(q)
	if err != nil {
		return nil, err
	}
	fingerprint := listFingerprint(q, f, sortKey)

	var pinned *domain.Date
	if q.Cursor != "" {
		c, err := decodeListCursor(q.Cursor)
		if err != nil {
			return nil, err
		}
		asOf, err := domain.ParseDate(c.AsOf)
		if err != nil || c.Sort != sortKey || c.Fingerprint != fingerprint {
			return nil, fmt.Errorf("%w: it was issued for other filters or another sort", ErrInvalidCursor)
		}
		if q.AsOf != nil && *q.AsOf != asOf {
			return nil, fmt.Errorf("%w: as_of differs from the cursor's", ErrInvalidCursor)
		}
		stockedOn, err := domain.ParseDate(c.StockedOn)
		if err != nil || c.VehicleID == "" {
			return nil, fmt.Errorf("%w: malformed position", ErrInvalidCursor)
		}
		pinned = &asOf
		f.After = &Keyset{StockedOn: stockedOn, VehicleID: c.VehicleID}
	} else if q.AsOf != nil {
		pinned = q.AsOf
	}
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
	page := &InventoryPage{AsOf: businessDate, Limit: limit}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		page.HasMore = true
		page.NextCursor = encode(listCursor{
			Version: 1, Sort: sortKey, AsOf: businessDate.String(), Fingerprint: fingerprint,
			StockedOn: last.StockedOn.String(), VehicleID: last.VehicleID,
		})
	}
	page.Vehicles = rows
	return page, nil
}

func (s *Service) GetVehicle(ctx context.Context, dealershipID, vehicleID string, asOf *domain.Date) (*VehicleResult, error) {
	businessDate, err := s.businessDate(ctx, dealershipID, asOf)
	if err != nil {
		return nil, err
	}
	v, err := s.rm.GetVehicle(ctx, dealershipID, vehicleID)
	if err != nil {
		return nil, err
	}
	return &VehicleResult{Vehicle: *v, AsOf: businessDate}, nil
}

type historyCursor struct {
	Version   int    `json:"v"`
	VehicleID string `json:"vid"`
	After     int    `json:"after"`
}

// GetActionHistory returns the vehicle's action changes, oldest first.
func (s *Service) GetActionHistory(ctx context.Context, dealershipID, vehicleID string, limit int, cursor string) (*HistoryPage, error) {
	limit, err := validateLimit(limit)
	if err != nil {
		return nil, err
	}
	after := 0
	if cursor != "" {
		var c historyCursor
		if err := decode(cursor, &c); err != nil || c.Version != 1 || c.VehicleID != vehicleID || c.After < 1 {
			return nil, fmt.Errorf("%w: not a history cursor for this vehicle", ErrInvalidCursor)
		}
		after = c.After
	}
	if _, err := s.dealershipLocation(ctx, dealershipID); err != nil {
		return nil, err
	}
	// Ownership: the vehicle must belong to this dealership.
	if _, err := s.rm.GetVehicle(ctx, dealershipID, vehicleID); err != nil {
		return nil, err
	}
	entries, err := s.rm.ListHistory(ctx, vehicleID, after, limit+1)
	if err != nil {
		return nil, err
	}
	page := &HistoryPage{Limit: limit}
	if len(entries) > limit {
		entries = entries[:limit]
		page.HasMore = true
		page.NextCursor = encode(historyCursor{Version: 1, VehicleID: vehicleID, After: entries[len(entries)-1].Version})
	}
	page.Entries = entries
	return page, nil
}

func (s *Service) GetInventorySummary(ctx context.Context, dealershipID string, asOf *domain.Date) (*SummaryResult, error) {
	businessDate, err := s.businessDate(ctx, dealershipID, asOf)
	if err != nil {
		return nil, err
	}
	sum, err := s.rm.Summary(ctx, dealershipID, businessDate)
	if err != nil {
		return nil, err
	}
	return &SummaryResult{Summary: sum, AsOf: businessDate}, nil
}

// businessDate is the pinned date when given, else today in the dealership's zone.
// It also verifies that the dealership exists.
func (s *Service) businessDate(ctx context.Context, dealershipID string, pinned *domain.Date) (domain.Date, error) {
	loc, err := s.dealershipLocation(ctx, dealershipID)
	if err != nil {
		return domain.Date{}, err
	}
	if pinned != nil {
		return *pinned, nil
	}
	return domain.BusinessDate(s.clock.Now(), loc), nil
}

func (s *Service) dealershipLocation(ctx context.Context, dealershipID string) (*time.Location, error) {
	return s.rm.DealershipLocation(ctx, dealershipID)
}

func validateLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return DefaultLimit, nil
	case limit < 1 || limit > MaxLimit:
		return 0, domain.Invalid("limit", fmt.Sprintf("must be between 1 and %d", MaxLimit))
	}
	return limit, nil
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

func decodeListCursor(s string) (listCursor, error) {
	var c listCursor
	if err := decode(s, &c); err != nil || c.Version != 1 {
		return c, fmt.Errorf("%w: malformed", ErrInvalidCursor)
	}
	return c, nil
}

func encode(v interface{}) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decode(s string, v interface{}) error {
	if len(s) > 512 {
		return fmt.Errorf("cursor too long")
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
