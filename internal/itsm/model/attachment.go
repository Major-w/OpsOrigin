package model

import "time"

type Attachment struct {
	ID         int64  `json:"id"`
	TicketID   string `json:"ticket_id"`
	TenantID   string `json:"tenant_id"`
	FileName   string `json:"file_name"`
	FileSize   int64  `json:"file_size"`
	FileType   string `json:"file_type"`
	UploadedBy string `json:"uploaded_by"`
	// ObjectKey 对象存储 key; 非空时二进制在对象存储, Data 为空; 空则二进制在 Data(历史/回退)。
	ObjectKey string    `json:"object_key"`
	Data      []byte    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}
