package repository

import (
	"context"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/otohacatalogmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type otohaCatalogRepository struct {
	client *dbent.Client
}

// NewOtohaCatalogRepository stores the Otoha model catalog (TASK-54).
func NewOtohaCatalogRepository(client *dbent.Client) service.OtohaCatalogRepository {
	return &otohaCatalogRepository{client: client}
}

func (r *otohaCatalogRepository) ListByGroup(ctx context.Context, groupID int64) ([]service.OtohaCatalogEntry, error) {
	rows, err := clientFromContext(ctx, r.client).OtohaCatalogModel.Query().
		Where(otohacatalogmodel.GroupIDEQ(groupID)).
		Order(
			dbent.Asc(otohacatalogmodel.FieldSortOrder),
			dbent.Asc(otohacatalogmodel.FieldID),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.OtohaCatalogEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, otohaCatalogEntityToService(row))
	}
	return out, nil
}

func (r *otohaCatalogRepository) Create(ctx context.Context, entry *service.OtohaCatalogEntry) error {
	if entry == nil {
		return service.ErrOtohaCatalogEntryNotFound
	}
	created, err := clientFromContext(ctx, r.client).OtohaCatalogModel.Create().
		SetGroupID(entry.GroupID).
		SetModelID(entry.ModelID).
		SetName(entry.Name).
		SetDescription(entry.Description).
		SetEnabled(entry.Enabled).
		SetSortOrder(entry.SortOrder).
		SetInputs(entry.Inputs).
		SetTools(entry.Tools).
		SetContextTokens(entry.Context).
		SetMaxOutputTokens(entry.MaxOutput).
		SetReasoningLevels(entry.Reasoning).
		SetDefaultReasoning(entry.DefaultReasoning).
		SetSpeed(entry.Speed).
		SetStrengths(entry.Strengths).
		SetComplexity(entry.Complexity).
		SetRoles(entry.Roles).
		SetUses(entry.Use).
		SetProfileSource(entry.ProfileSource).
		SetCostTier(entry.CostTier).
		Save(ctx)
	if err != nil {
		return translatePersistenceError(err, nil, service.ErrOtohaCatalogEntryExists)
	}
	*entry = otohaCatalogEntityToService(created)
	return nil
}

func (r *otohaCatalogRepository) Update(ctx context.Context, entry *service.OtohaCatalogEntry) error {
	if entry == nil {
		return service.ErrOtohaCatalogEntryNotFound
	}
	update := clientFromContext(ctx, r.client).OtohaCatalogModel.UpdateOneID(entry.ID).
		SetModelID(entry.ModelID).
		SetName(entry.Name).
		SetDescription(entry.Description).
		SetEnabled(entry.Enabled).
		SetSortOrder(entry.SortOrder).
		SetTools(entry.Tools).
		SetContextTokens(entry.Context).
		SetMaxOutputTokens(entry.MaxOutput).
		SetDefaultReasoning(entry.DefaultReasoning).
		SetSpeed(entry.Speed).
		SetComplexity(entry.Complexity).
		SetProfileSource(entry.ProfileSource).
		SetCostTier(entry.CostTier)
	update = setOtohaJSONFields(update, entry)
	updated, err := update.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrOtohaCatalogEntryNotFound, service.ErrOtohaCatalogEntryExists)
	}
	*entry = otohaCatalogEntityToService(updated)
	return nil
}

func setOtohaJSONFields(update *dbent.OtohaCatalogModelUpdateOne, entry *service.OtohaCatalogEntry) *dbent.OtohaCatalogModelUpdateOne {
	if entry.Inputs != nil {
		update = update.SetInputs(entry.Inputs)
	} else {
		update = update.ClearInputs()
	}
	if entry.Reasoning != nil {
		update = update.SetReasoningLevels(entry.Reasoning)
	} else {
		update = update.ClearReasoningLevels()
	}
	if entry.Strengths != nil {
		update = update.SetStrengths(entry.Strengths)
	} else {
		update = update.ClearStrengths()
	}
	if entry.Roles != nil {
		update = update.SetRoles(entry.Roles)
	} else {
		update = update.ClearRoles()
	}
	if entry.Use != nil {
		update = update.SetUses(entry.Use)
	} else {
		update = update.ClearUses()
	}
	return update
}

func (r *otohaCatalogRepository) Delete(ctx context.Context, id int64) error {
	err := clientFromContext(ctx, r.client).OtohaCatalogModel.DeleteOneID(id).Exec(ctx)
	return translatePersistenceError(err, service.ErrOtohaCatalogEntryNotFound, nil)
}

// UpdateSortOrders sets the orders in one transaction; an ID that is not the group's fails it.
func (r *otohaCatalogRepository) UpdateSortOrders(ctx context.Context, groupID int64, orders map[int64]int) error {
	if len(orders) == 0 {
		return nil
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for id, order := range orders {
		affected, err := tx.OtohaCatalogModel.Update().
			Where(otohacatalogmodel.IDEQ(id), otohacatalogmodel.GroupIDEQ(groupID)).
			SetSortOrder(order).
			Save(ctx)
		if err != nil {
			return err
		}
		if affected == 0 {
			return service.ErrOtohaCatalogEntryNotFound
		}
	}
	return tx.Commit()
}

func otohaCatalogEntityToService(row *dbent.OtohaCatalogModel) service.OtohaCatalogEntry {
	return service.OtohaCatalogEntry{
		ID:               row.ID,
		GroupID:          row.GroupID,
		ModelID:          row.ModelID,
		Name:             row.Name,
		Description:      row.Description,
		Enabled:          row.Enabled,
		SortOrder:        row.SortOrder,
		Inputs:           row.Inputs,
		Tools:            row.Tools,
		Context:          row.ContextTokens,
		MaxOutput:        row.MaxOutputTokens,
		Reasoning:        row.ReasoningLevels,
		DefaultReasoning: row.DefaultReasoning,
		Speed:            row.Speed,
		Strengths:        row.Strengths,
		Complexity:       row.Complexity,
		Roles:            row.Roles,
		Use:              row.Uses,
		ProfileSource:    row.ProfileSource,
		CostTier:         row.CostTier,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}
