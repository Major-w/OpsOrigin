package model

import "time"

type Attachment struct {
	ID         int64     `json:"id"`
	TicketID   string    `json:"ticket_id"`
	TenantID   string    `json:"tenant_id"`
	FileName   string    `json:"file_name"`
	FileSize   int64     `json:"file_size"`
	FileType   string    `json:"file_type"`
	UploadedBy string    `json:"uploaded_by"`
	Data       []byte    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}
