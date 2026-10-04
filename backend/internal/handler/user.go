package handler

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/service"
)

type UserHandler struct {
	svc *service.UserService
}

func NewUserHandler(svc *service.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

type createUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (r *createUserRequest) Validate() error {
	r.Name = strings.TrimSpace(r.Name)
	r.Email = strings.TrimSpace(r.Email)

	if r.Name == "" {
		return invalidParam("姓名不能为空")
	}
	if utf8.RuneCountInString(r.Name) > 64 {
		return invalidParam("姓名长度不能超过 64 个字符")
	}
	if r.Email == "" {
		return invalidParam("邮箱不能为空")
	}
	if len(r.Email) > 255 {
		return invalidParam("邮箱长度不能超过 255 个字符")
	}
	// mail.ParseAddress 也接受 "Alice <a@x.com>" 这种写法，所以要求解析结果与原文一致
	if addr, err := mail.ParseAddress(r.Email); err != nil || addr.Address != r.Email {
		return invalidParam("邮箱格式不正确")
	}
	return nil
}

// Create POST /api/users
func (h *UserHandler) Create(c *echo.Context) error {
	req, err := bind[createUserRequest](c)
	if err != nil {
		return err
	}

	user, err := h.svc.Create(c.Request().Context(), repository.CreateUserParams{
		Name:  req.Name,
		Email: req.Email,
	})
	if err != nil {
		return err
	}
	return response.Created(c, user)
}

type getUserRequest struct {
	ID int64 `param:"id"`
}

func (r *getUserRequest) Validate() error {
	if r.ID < 1 {
		return invalidParam("用户 ID 不合法")
	}
	return nil
}

// Get GET /api/users/:id
func (h *UserHandler) Get(c *echo.Context) error {
	req, err := bind[getUserRequest](c)
	if err != nil {
		return err
	}

	user, err := h.svc.Get(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	return response.OK(c, user)
}

type listUsersRequest struct {
	Page     int32 `query:"page"`
	PageSize int32 `query:"pageSize"`
}

func (r *listUsersRequest) Validate() error {
	// 未传时使用默认值
	if r.Page == 0 {
		r.Page = 1
	}
	if r.PageSize == 0 {
		r.PageSize = 20
	}

	if r.Page < 1 {
		return invalidParam("页码必须大于 0")
	}
	if r.PageSize < 1 || r.PageSize > 100 {
		return invalidParam("每页条数必须在 1 到 100 之间")
	}
	return nil
}

// List GET /api/users?page=1&pageSize=20
func (h *UserHandler) List(c *echo.Context) error {
	req, err := bind[listUsersRequest](c)
	if err != nil {
		return err
	}

	users, total, err := h.svc.List(c.Request().Context(), req.Page, req.PageSize)
	if err != nil {
		return err
	}
	return response.OK(c, response.NewPage(users, total, req.Page, req.PageSize))
}
