package repository

import (
	"context"
	"fmt"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/otohacatalogimagemodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type otohaCatalogImageRepository struct {
	client *dbent.Client
}

// NewOtohaCatalogImageRepository stores the image models of the Otoha model catalog (TASK-73).
func NewOtohaCatalogImageRepository(client *dbent.Client) service.OtohaImageRepository {
	return &otohaCatalogImageRepository{client: client}
}

func (r *otohaCatalogImageRepository) ListImagesByGroup(ctx context.Context, groupID int64) ([]service.OtohaImageEntry, error) {
	rows, err := clientFromContext(ctx, r.client).OtohaCatalogImageModel.Query().
		Where(otohacatalogimagemodel.GroupIDEQ(groupID)).
		Order(
			dbent.Asc(otohacatalogimagemodel.FieldSortOrder),
			dbent.Asc(otohacatalogimagemodel.FieldID),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.OtohaImageEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, otohaImageEntityToService(row))
	}
	return out, nil
}

func (r *otohaCatalogImageRepository) CreateImage(ctx context.Context, entry *service.OtohaImageEntry) error {
	if entry == nil {
		return service.ErrOtohaCatalogEntryNotFound
	}
	created, err := clientFromContext(ctx, r.client).OtohaCatalogImageModel.Create().
		SetGroupID(entry.GroupID).
		SetModelID(entry.ModelID).
		SetName(entry.Name).
		SetDescription(entry.Description).
		SetEnabled(entry.Enabled).
		SetSortOrder(entry.SortOrder).
		SetSizes(entry.Sizes).
		SetQualities(entry.Qualities).
		SetEdits(entry.Edits).
		SetNillablePrice1k(entry.Price1K).
		SetNillablePrice2k(entry.Price2K).
		SetNillablePrice4k(entry.Price4K).
		SetSource(sourceOrManual(entry.Source)).
		SetManualFields(entry.ManualFields).
		SetNillableUnavailableSince(entry.UnavailableSince).
		Save(ctx)
	if err != nil {
		return translatePersistenceError(err, nil, service.ErrOtohaCatalogEntryExists)
	}
	*entry = otohaImageEntityToService(created)
	return nil
}

func (r *otohaCatalogImageRepository) UpdateImage(ctx context.Context, entry *service.OtohaImageEntry) error {
	if entry == nil {
		return service.ErrOtohaCatalogEntryNotFound
	}
	update := clientFromContext(ctx, r.client).OtohaCatalogImageModel.UpdateOneID(entry.ID).
		SetModelID(entry.ModelID).
		SetName(entry.Name).
		SetDescription(entry.Description).
		SetEnabled(entry.Enabled).
		SetSortOrder(entry.SortOrder).
		SetSizes(entry.Sizes).
		SetQualities(entry.Qualities).
		SetEdits(entry.Edits).
		SetSource(sourceOrManual(entry.Source)).
		SetManualFields(entry.ManualFields)
	if entry.UnavailableSince != nil {
		update = update.SetUnavailableSince(*entry.UnavailableSince)
	} else {
		update = update.ClearUnavailableSince()
	}
	if entry.Price1K != nil {
		update = update.SetPrice1k(*entry.Price1K)
	} else {
		update = update.ClearPrice1k()
	}
	if entry.Price2K != nil {
		update = update.SetPrice2k(*entry.Price2K)
	} else {
		update = update.ClearPrice2k()
	}
	if entry.Price4K != nil {
		update = update.SetPrice4k(*entry.Price4K)
	} else {
		update = update.ClearPrice4k()
	}
	updated, err := update.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrOtohaCatalogEntryNotFound, service.ErrOtohaCatalogEntryExists)
	}
	*entry = otohaImageEntityToService(updated)
	return nil
}

func (r *otohaCatalogImageRepository) DeleteImage(ctx context.Context, id int64) error {
	err := clientFromContext(ctx, r.client).OtohaCatalogImageModel.DeleteOneID(id).Exec(ctx)
	return translatePersistenceError(err, service.ErrOtohaCatalogEntryNotFound, nil)
}

func sourceOrManual(source string) string {
	if source == "" {
		return service.OtohaImageSourceManual
	}
	return source
}

func otohaImageEntityToService(row *dbent.OtohaCatalogImageModel) service.OtohaImageEntry {
	manualFields := row.ManualFields
	if manualFields == nil {
		manualFields = []string{}
	}
	return service.OtohaImageEntry{
		ID:          row.ID,
		GroupID:     row.GroupID,
		ModelID:     row.ModelID,
		Name:        row.Name,
		Description: row.Description,
		Enabled:     row.Enabled,
		SortOrder:   row.SortOrder,
		Sizes:       row.Sizes,
		Qualities:   row.Qualities,
		Edits:       row.Edits,
		Price1K:     row.Price1k,
		Price2K:     row.Price2k,
		Price4K:     row.Price4k,

		Source:           row.Source,
		ManualFields:     manualFields,
		UnavailableSince: row.UnavailableSince,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

var _ service.OtohaImageObservations = (*otohaCatalogImageRepository)(nil)

// ObservedImageOutputs lists, for the group's image requests since a time that billing charged by the size of the
// output, each distinct model (as the client named it), size asked for, billed tier and output size, most recent
// first. The catalog prices a size at the highest of these tiers (TASK-77).
func (r *otohaCatalogImageRepository) ObservedImageOutputs(ctx context.Context, groupID int64, since time.Time) ([]service.OtohaImageObservation, error) {
	const query = `
SELECT COALESCE(NULLIF(requested_model, ''), model) AS model_name,
       LOWER(BTRIM(image_input_size)) AS requested_size,
       image_size,
       COALESCE(BTRIM(image_output_size), '') AS output_size,
       MAX(created_at) AS last_seen
FROM usage_logs
WHERE group_id = $1
  AND created_at >= $2
  AND image_size_source = 'output'
  AND image_size IN ('1K', '2K', '4K')
  AND image_input_size IS NOT NULL
  AND BTRIM(image_input_size) <> ''
GROUP BY 1, 2, 3, 4
ORDER BY last_seen DESC, model_name, requested_size, image_size DESC, output_size`
	var rows entsql.Rows
	if err := clientFromContext(ctx, r.client).Driver().Query(ctx, query, []any{groupID, since}, &rows); err != nil {
		return nil, fmt.Errorf("read billed image tiers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []service.OtohaImageObservation{}
	for rows.Next() {
		var observation service.OtohaImageObservation
		var lastSeen time.Time
		if err := rows.Scan(&observation.Model, &observation.RequestedSize, &observation.Tier, &observation.OutputSize, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan billed image tier: %w", err)
		}
		out = append(out, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read billed image tiers: %w", err)
	}
	return out, nil
}
