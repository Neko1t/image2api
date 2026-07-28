package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"backend/internal/model"
	"backend/internal/repo"
	"gorm.io/gorm"
)

type CDKService struct {
	cdks     *repo.CDKRepository
	users    *repo.UserRepository
	settings *repo.SiteSettingRepository
	orders   *repo.OrderRepository
}

func NewCDKService(cdks *repo.CDKRepository, users *repo.UserRepository, settings *repo.SiteSettingRepository, orders *repo.OrderRepository) *CDKService {
	return &CDKService{
		cdks:     cdks,
		users:    users,
		settings: settings,
		orders:   orders,
	}
}

func (s *CDKService) List(ctx context.Context) ([]model.CDKCode, map[string]any, map[string]string, error) {
	items, err := s.cdks.List(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	stats, err := s.cdks.Stats(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	// Build an id -> display name map (name, else email, else id) so the handler
	// can annotate redeemed codes with redeemed_by_name (mirrors admin.py).
	users, err := s.users.List(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	nameByID := make(map[string]string, len(users))
	for _, u := range users {
		name := strings.TrimSpace(u.Name)
		if name == "" {
			name = strings.TrimSpace(u.Email)
		}
		if name == "" {
			name = u.ID
		}
		nameByID[u.ID] = name
	}
	return items, stats, nameByID, nil
}

func normalizeCDKType(t string) string {
	if strings.EqualFold(strings.TrimSpace(t), "marketing") {
		return "marketing"
	}
	return "normal"
}

func (s *CDKService) Generate(ctx context.Context, amount, count int, note, cdkType string) ([]model.CDKCode, error) {
	if amount <= 0 {
		return nil, errors.New("金额必须大于 0")
	}
	if count < 1 {
		count = 1
	}
	if count > 500 {
		count = 500
	}

	cdkType = normalizeCDKType(cdkType)
	// One batch id per generate call — marketing codes are "one per user per
	// batch", so codes created together must share it.
	batchID := randomUpper(20)
	items := make([]model.CDKCode, 0, count)
	for i := 0; i < count; i++ {
		items = append(items, model.CDKCode{
			Code:      randomCDK(),
			Amount:    amount,
			Status:    "active",
			Type:      cdkType,
			BatchID:   batchID,
			Note:      strings.TrimSpace(note),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		})
	}
	if err := s.cdks.CreateBatch(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *CDKService) Delete(ctx context.Context, code string) error {
	rows, err := s.cdks.Delete(ctx, strings.TrimSpace(strings.ToUpper(code)))
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteBulk removes many CDK codes in one call (multi-select).
func (s *CDKService) DeleteBulk(ctx context.Context, codes []string) (int, error) {
	seen := make(map[string]struct{}, len(codes))
	clean := make([]string, 0, len(codes))
	for _, code := range codes {
		code = strings.TrimSpace(strings.ToUpper(code))
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		clean = append(clean, code)
	}
	if len(clean) == 0 {
		return 0, nil
	}
	rows, err := s.cdks.DeleteByCodes(ctx, clean)
	return int(rows), err
}

func (s *CDKService) Redeem(ctx context.Context, userID, code string) (map[string]any, error) {
	// Honor the admin "兑换码" switch — when off, no code can be redeemed.
	if s.settings != nil {
		if v, _ := s.settings.GetValue(ctx, "credits.cdk_redeem_enabled"); v == "false" {
			return nil, errors.New("兑换功能已关闭")
		}
	}
	code = strings.TrimSpace(strings.ToUpper(code))
	if code == "" {
		return nil, errors.New("请输入兑换码")
	}

	item, err := s.cdks.Redeem(ctx, code, userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.New("兑换码无效")
		}
		if errors.Is(err, repo.ErrCDKBatchLimit) {
			return nil, errors.New("该营销活动每人限兑一次,你已兑换过本批次的兑换码")
		}
		if err == gorm.ErrDuplicatedKey {
			return nil, errors.New("兑换码已被使用")
		}
		return nil, err
	}

	// Atomic, row-locked credit grant — never read-modify-write the balance, or a
	// concurrent debit/redeem would clobber it (lost update).
	updated, err := s.users.AdjustCredits(ctx, userID, float64(item.Amount))
	if err != nil {
		return nil, err
	}
	RecordCreditOrder(ctx, s.orders, userID, float64(item.Amount), "cdk", "兑换码 "+item.Code)

	return map[string]any{
		"amount":  item.Amount,
		"credits": updated.Credits,
	}, nil
}

func randomCDK() string {
	seg := func() string {
		return randomUpper(4)
	}
	return seg() + "-" + seg() + "-" + seg() + "-" + seg()
}
