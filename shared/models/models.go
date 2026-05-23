package models

import "time"

type NameStatus string

const (
	NameStatusActive        NameStatus = "active"
	NameStatusPendingReview NameStatus = "pending_review"
	NameStatusOffensive     NameStatus = "offensive"
	NameStatusRemoved       NameStatus = "removed"
)

type Name struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	Text          string     `gorm:"size:80;uniqueIndex:idx_names_text_lower,expression:lower(text);not null" json:"text"`
	Status        NameStatus `gorm:"size:16;not null;default:'active';index" json:"status"`
	SubmitterHash string     `gorm:"size:64;index" json:"-"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type Vote struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	NameID    uint      `gorm:"not null;uniqueIndex:idx_votes_name_voter,priority:1" json:"name_id"`
	VoterHash string    `gorm:"size:64;not null;uniqueIndex:idx_votes_name_voter,priority:2" json:"-"`
	Value     int8      `gorm:"not null" json:"value"` // +1 or -1
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NameFlag records that a voter has flagged a name as offensive. The composite
// unique index makes flag inserts idempotent per voter, so the live count is
// always derivable via COUNT(*) without a denormalised column on Name.
type NameFlag struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	NameID    uint      `gorm:"not null;uniqueIndex:idx_flags_name_voter,priority:1;index" json:"name_id"`
	VoterHash string    `gorm:"size:64;not null;uniqueIndex:idx_flags_name_voter,priority:2" json:"-"`
	CreatedAt time.Time `json:"created_at"`
}
