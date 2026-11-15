package state

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type fosterRecordRow struct {
	Repo       string `gorm:"column:repo;primaryKey"`
	Collection string `gorm:"column:collection;primaryKey"`
	Rkey       string `gorm:"column:rkey;primaryKey"`
	Cid        string `gorm:"column:cid"`
	Value      []byte `gorm:"column:value"`
}

func (fosterRecordRow) TableName() string { return "foster_records" }

type handoverRow struct {
	Repo    string `gorm:"column:repo;primaryKey"`
	AckedAt int64  `gorm:"column:acked_at"`
}

func (handoverRow) TableName() string { return "handovers" }

// FosterRecord is a record taken over from tangled's foster knot. Value is
// the record's atproto JSON and Cid the CID the foster served it at, which
// snot must reproduce for the foster to retire its shadow.
type FosterRecord struct {
	Collection string
	Rkey       string
	Cid        string
	Value      []byte
}

// Handovers tracks which repo DIDs have been taken over from the foster knot
// and the records that came with them.
type Handovers struct {
	db *gorm.DB
}

// Handovers returns the foster-handover accessor.
func (db *DB) Handovers() *Handovers { return &Handovers{db: db.gorm} }

// PutRecords replaces the records adopted for a repo DID.
func (h *Handovers) PutRecords(repoDid string, recs []FosterRecord) error {
	return h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&fosterRecordRow{}, "repo = ?", repoDid).Error; err != nil {
			return err
		}
		for _, r := range recs {
			row := fosterRecordRow{Repo: repoDid, Collection: r.Collection, Rkey: r.Rkey, Cid: r.Cid, Value: r.Value}
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Records returns the records adopted for a repo DID.
func (h *Handovers) Records(repoDid string) ([]FosterRecord, error) {
	var rows []fosterRecordRow
	if err := h.db.Order("collection, rkey").Find(&rows, "repo = ?", repoDid).Error; err != nil {
		return nil, err
	}
	recs := make([]FosterRecord, len(rows))
	for i, r := range rows {
		recs[i] = FosterRecord{Collection: r.Collection, Rkey: r.Rkey, Cid: r.Cid, Value: r.Value}
	}
	return recs, nil
}

// Settled reports whether a repo DID's foster era is over: the foster acked
// the handover, or never shadowed the repo.
func (h *Handovers) Settled(repoDid string) (bool, error) {
	var n int64
	err := h.db.Model(&handoverRow{}).Where("repo = ?", repoDid).Count(&n).Error
	return n > 0, err
}

// MarkSettled records that a repo DID needs no further handover.
func (h *Handovers) MarkSettled(repoDid string, at int64) error {
	return h.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&handoverRow{Repo: repoDid, AckedAt: at}).Error
}
