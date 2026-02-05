package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

func (d *Database) CreatePublishRecords(
	ctx context.Context,
	txHash string,
	items []map[string]interface{},
) error {
	records := make([]PublishRecord, 0, len(items))
	for _, item := range items {
		objectID, _ := item["object_id"].(string)
		if objectID == "" {
			continue
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("marshal publish item: %w", err)
		}
		records = append(records, PublishRecord{
			ObjectID: objectID,
			Data:     raw,
			TxHash:   txHash,
			Status:   PublishStatusPending,
		})
	}
	if len(records) == 0 {
		return nil
	}
	return d.db.WithContext(ctx).Create(&records).Error
}

func (d *Database) ConfirmPublishRecords(ctx context.Context, txHash string) error {
	return d.db.WithContext(ctx).
		Model(&PublishRecord{}).
		Where("tx_hash = ? AND status = ?", txHash, PublishStatusPending).
		Update("status", PublishStatusConfirmed).Error
}

type PublishHistoryFilter struct {
	ObjectID      string
	TxHash        string
	From          *time.Time
	To            *time.Time
	ExpandHistory bool
	Limit         int
	Offset        int
}

type PublishHistoryResult struct {
	Records []PublishRecord `json:"records"`
	Total   int64           `json:"total"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
}

func (d *Database) QueryPublishHistory(
	ctx context.Context,
	filter PublishHistoryFilter,
) (*PublishHistoryResult, error) {
	query := d.db.WithContext(ctx).Model(&PublishRecord{})

	if filter.ObjectID != "" {
		query = query.Where("object_id = ?", filter.ObjectID)
	}
	if filter.TxHash != "" {
		query = query.Where("tx_hash = ?", filter.TxHash)
	}
	if filter.From != nil {
		query = query.Where("created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		query = query.Where("created_at <= ?", *filter.To)
	}

	if !filter.ExpandHistory {
		return d.queryLatestPerObject(ctx, query, filter)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count publish records: %w", err)
	}

	var records []PublishRecord
	if err := query.
		Order("created_at DESC").
		Limit(filter.Limit).
		Offset(filter.Offset).
		Find(&records).Error; err != nil {
		return nil, fmt.Errorf("query publish records: %w", err)
	}

	return &PublishHistoryResult{
		Records: records,
		Total:   total,
		Limit:   filter.Limit,
		Offset:  filter.Offset,
	}, nil
}

func (d *Database) queryLatestPerObject(
	ctx context.Context,
	baseQuery *gorm.DB,
	filter PublishHistoryFilter,
) (*PublishHistoryResult, error) {
	subQuery := d.db.WithContext(ctx).
		Model(&PublishRecord{}).
		Select("DISTINCT ON (object_id) *")

	if filter.ObjectID != "" {
		subQuery = subQuery.Where("object_id = ?", filter.ObjectID)
	}
	if filter.TxHash != "" {
		subQuery = subQuery.Where("tx_hash = ?", filter.TxHash)
	}
	if filter.From != nil {
		subQuery = subQuery.Where("created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		subQuery = subQuery.Where("created_at <= ?", *filter.To)
	}

	subQuery = subQuery.Order("object_id, created_at DESC")

	var total int64
	countQuery := d.db.WithContext(ctx).
		Table("(?) AS sub", subQuery).
		Count(&total)
	if countQuery.Error != nil {
		return nil, fmt.Errorf("count latest publish records: %w", countQuery.Error)
	}

	var records []PublishRecord
	if err := d.db.WithContext(ctx).
		Table("(?) AS sub", subQuery).
		Order("sub.created_at DESC").
		Limit(filter.Limit).
		Offset(filter.Offset).
		Find(&records).Error; err != nil {
		return nil, fmt.Errorf("query latest publish records: %w", err)
	}

	return &PublishHistoryResult{
		Records: records,
		Total:   total,
		Limit:   filter.Limit,
		Offset:  filter.Offset,
	}, nil
}
