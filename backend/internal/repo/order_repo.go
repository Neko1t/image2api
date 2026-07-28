package repo

import (
	"context"
	"strings"
	"time"

	"backend/internal/model"
	"gorm.io/gorm"
)

type OrderRepository struct{ db *gorm.DB }

func NewOrderRepository(db *gorm.DB) *OrderRepository { return &OrderRepository{db: db} }

func (r *OrderRepository) Create(ctx context.Context, o *model.Order) error {
	return r.db.WithContext(ctx).Create(o).Error
}

func (r *OrderRepository) Get(ctx context.Context, id string) (*model.Order, error) {
	var o model.Order
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&o).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *OrderRepository) Update(ctx context.Context, id string, patch map[string]any) error {
	return r.db.WithContext(ctx).Model(&model.Order{}).Where("id = ?", id).Updates(patch).Error
}

// ListByUser returns a user's own orders, newest first, with pagination + total.
// query — server-side search over 订单号 / 支付方式 / 金额 (跨页).
func (r *OrderRepository) ListByUser(ctx context.Context, userID, status, query string, limit, offset int) ([]model.Order, int64, error) {
	var out []model.Order
	var total int64
	q := r.db.WithContext(ctx).Model(&model.Order{}).Where("user_id = ?", userID).Where("source = ?", "epay")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if term := strings.TrimSpace(query); term != "" {
		like := "%" + term + "%"
		q = q.Where("(id ILIKE ? OR method ILIKE ? OR CAST(amount AS TEXT) LIKE ?)", like, like, like)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 20
	}
	err := q.Order("created_at desc").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}

// List returns all orders (admin) with optional status/source filter +
// pagination. query — server-side search over 订单号 / 支付方式 / 金额; userIDs —
// additionally match orders belonging to these users (resolved from a 用户名
// search upstream).
func (r *OrderRepository) List(ctx context.Context, status, source, query string, userIDs []string, limit, offset int) ([]model.Order, int64, error) {
	var out []model.Order
	var total int64
	q := r.db.WithContext(ctx).Model(&model.Order{})
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if source != "" {
		q = q.Where("source = ?", source)
	}
	if term := strings.TrimSpace(query); term != "" {
		like := "%" + term + "%"
		if len(userIDs) > 0 {
			q = q.Where("(id ILIKE ? OR method ILIKE ? OR CAST(amount AS TEXT) LIKE ? OR user_id IN ?)", like, like, like, userIDs)
		} else {
			q = q.Where("(id ILIKE ? OR method ILIKE ? OR CAST(amount AS TEXT) LIKE ?)", like, like, like)
		}
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 50
	}
	err := q.Order("created_at desc").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}

// MarkPaid flips a pending order to paid atomically. Returns true only on the
// transition pending→paid, so a duplicate notify can never double-credit.
func (r *OrderRepository) MarkPaid(ctx context.Context, id, tradeNo string, paidAt time.Time) (bool, error) {
	res := r.db.WithContext(ctx).Model(&model.Order{}).
		Where("id = ? AND status = ?", id, "pending").
		Updates(map[string]any{"status": "paid", "paid_at": paidAt, "trade_no": tradeNo})
	return res.RowsAffected > 0, res.Error
}

// ExpirePending cancels every still-pending order whose ExpiresAt has passed and
// returns how many it cancelled.
func (r *OrderRepository) ExpirePending(ctx context.Context, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Order{}).
		Where("status = ? AND expires_at < ?", "pending", now).
		Updates(map[string]any{"status": "cancelled"})
	return res.RowsAffected, res.Error
}
