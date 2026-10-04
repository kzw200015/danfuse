package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
	"github.com/kzw200015/danfuse/backend/internal/repository"
)

type UserService struct {
	q repository.Querier
}

func NewUserService(q repository.Querier) *UserService {
	return &UserService{q: q}
}

func (s *UserService) Create(ctx context.Context, arg repository.CreateUserParams) (repository.User, error) {
	user, err := s.q.CreateUser(ctx, arg)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return repository.User{}, errcode.ErrUserEmailExists.Wrap(err)
		}
		return repository.User{}, fmt.Errorf("create user: %w", err)
	}
	return user, nil
}

func (s *UserService) Get(ctx context.Context, id int64) (repository.User, error) {
	user, err := s.q.GetUser(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.User{}, errcode.ErrUserNotFound
		}
		return repository.User{}, fmt.Errorf("get user %d: %w", id, err)
	}
	return user, nil
}

// List 分页查询，page 从 1 开始。
func (s *UserService) List(ctx context.Context, page, pageSize int32) ([]repository.User, int64, error) {
	total, err := s.q.CountUsers(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}
	users, err := s.q.ListUsers(ctx, repository.ListUsersParams{
		Limit:  pageSize,
		Offset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	return users, total, nil
}
