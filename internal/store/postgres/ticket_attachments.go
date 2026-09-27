package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrAttachmentNotFound hides whether an attachment exists on another
// customer's ticket.
var ErrAttachmentNotFound = errors.New("attachment not found")

// TicketAttachment describes an uploaded image; the bytes are served by a
// separate endpoint.
type TicketAttachment struct {
	ID          string `json:"id"`
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	SizeBytes   int    `json:"size_bytes"`
}

// AttachmentUpload is one validated image to store with a message.
type AttachmentUpload struct {
	FileName    string
	ContentType string
	Data        []byte
}

// attachmentPlaceholder stands in for the text of an image-only message.
const attachmentPlaceholder = "（图片附件）"

func insertAttachments(ctx context.Context, tx pgx.Tx, ticketID, messageID string, uploads []AttachmentUpload) ([]TicketAttachment, error) {
	result := make([]TicketAttachment, 0, len(uploads))
	for _, upload := range uploads {
		item := TicketAttachment{FileName: upload.FileName, ContentType: upload.ContentType, SizeBytes: len(upload.Data)}
		if err := tx.QueryRow(ctx, `
			INSERT INTO support_attachments(ticket_id,message_id,file_name,content_type,size_bytes,data)
			VALUES($1,$2,$3,$4,$5,$6) RETURNING id
		`, ticketID, messageID, upload.FileName, upload.ContentType, len(upload.Data), upload.Data).Scan(&item.ID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// attachmentsByMessage loads attachment metadata for a ticket's messages.
func (s *OperationsStore) attachmentsByMessage(ctx context.Context, ticketID string) (map[string][]TicketAttachment, error) {
	rows, err := s.db.Query(ctx, `SELECT message_id,id,file_name,content_type,size_bytes FROM support_attachments WHERE ticket_id=$1 ORDER BY created_at`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]TicketAttachment{}
	for rows.Next() {
		var messageID string
		var item TicketAttachment
		if err := rows.Scan(&messageID, &item.ID, &item.FileName, &item.ContentType, &item.SizeBytes); err != nil {
			return nil, err
		}
		result[messageID] = append(result[messageID], item)
	}
	return result, rows.Err()
}

// TicketAttachmentData returns an attachment's bytes. accountID limits the
// lookup to a customer's own tickets, and customers never see attachments of
// internal staff notes.
func (s *OperationsStore) TicketAttachmentData(ctx context.Context, ticketID, attachmentID, accountID string) (TicketAttachment, []byte, error) {
	var item TicketAttachment
	var data []byte
	err := s.db.QueryRow(ctx, `
		SELECT a.id,a.file_name,a.content_type,a.size_bytes,a.data
		FROM support_attachments a
		JOIN support_messages m ON m.id=a.message_id
		JOIN support_tickets t ON t.id=a.ticket_id
		WHERE a.id=$1 AND a.ticket_id=$2 AND ($3='' OR (t.account_id::text=$3 AND m.internal=false))
	`, attachmentID, ticketID, accountID).Scan(&item.ID, &item.FileName, &item.ContentType, &item.SizeBytes, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return TicketAttachment{}, nil, ErrAttachmentNotFound
	}
	return item, data, err
}
