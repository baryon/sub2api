package repository

import (
	"context"

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
		SetEdits(entry.Edits)
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

func otohaImageEntityToService(row *dbent.OtohaCatalogImageModel) service.OtohaImageEntry {
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
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}
