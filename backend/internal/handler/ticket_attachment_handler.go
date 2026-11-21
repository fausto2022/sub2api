package handler

import (
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// TicketAttachmentHandler 处理用户侧工单附件：上传、鉴权读取、删除未绑定附件。
//
// 读取不返回公开静态地址：所有字节都经本 handler 做权限判断后代理输出
// （design.md §1.2 存储选型：本地磁盘 + 鉴权代理）。
type TicketAttachmentHandler struct {
	service *service.TicketAttachmentService
}

func NewTicketAttachmentHandler(attachmentService *service.TicketAttachmentService) *TicketAttachmentHandler {
	return &TicketAttachmentHandler{service: attachmentService}
}

// Upload 处理单文件 multipart 上传。
// POST /api/v1/tickets/attachments  (form field: file)
func (h *TicketAttachmentHandler) Upload(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	// 先限制整个请求体（3×5MB + 余量），避免超大 body 占满内存/磁盘。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.TicketAttachmentMaxRequestBytes)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "Invalid request: a single image is required in form field 'file'")
		return
	}
	if fileHeader.Size > service.TicketAttachmentMaxBytes {
		response.ErrorFrom(c, service.ErrTicketAttachmentTooLarge)
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		response.BadRequest(c, "Invalid request: uploaded file cannot be read")
		return
	}
	defer func() { _ = file.Close() }()

	// 多读 1 字节用于判定「实际超过上限」，不信任 multipart 的 Size 字段。
	data, err := io.ReadAll(io.LimitReader(file, service.TicketAttachmentMaxBytes+1))
	if err != nil {
		response.BadRequest(c, "Invalid request: uploaded file cannot be read")
		return
	}

	item, err := h.service.Upload(
		c.Request.Context(),
		subject.UserID,
		fileHeader.Filename,
		fileHeader.Header.Get("Content-Type"),
		data,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, dto.TicketAttachmentResponse{Attachment: dto.TicketAttachmentFromService(item)})
}

// Get 鉴权代理读取附件字节。
// GET /api/v1/tickets/attachments/:id
func (h *TicketAttachmentHandler) Get(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	attachmentID, ok := parseTicketAttachmentID(c)
	if !ok {
		return
	}
	item, data, err := h.service.GetForRead(c.Request.Context(), ticketAttachmentSubject(c, subject.UserID), attachmentID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// 私密缓存 + 禁止嗅探：不收 SVG，配合 nosniff 可安全地内联展示。
	c.Header("Content-Type", item.ContentType)
	c.Header("Content-Length", strconv.FormatInt(item.ByteSize, 10))
	c.Header("Cache-Control", "private, max-age=3600")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", "inline")
	c.Data(http.StatusOK, item.ContentType, data)
}

// Delete 删除未绑定附件（已绑定返回 409）。
// DELETE /api/v1/tickets/attachments/:id
func (h *TicketAttachmentHandler) Delete(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	attachmentID, ok := parseTicketAttachmentID(c)
	if !ok {
		return
	}
	if err := h.service.DeleteUnbound(c.Request.Context(), ticketAttachmentSubject(c, subject.UserID), attachmentID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.TicketAttachmentDeleteResponse{Deleted: true, ID: attachmentID})
}

// ticketAttachmentSubject 组合请求者身份：用户侧路由以 JWT 角色判定管理员。
func ticketAttachmentSubject(c *gin.Context, userID int64) service.TicketAttachmentSubject {
	role, _ := middleware2.GetUserRoleFromContext(c)
	return service.TicketAttachmentSubject{
		UserID:  userID,
		IsAdmin: role == service.RoleAdmin,
	}
}

func parseTicketAttachmentID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid ticket attachment ID")
		return 0, false
	}
	return id, true
}
