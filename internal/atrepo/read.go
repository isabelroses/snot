package atrepo

import (
	"errors"

	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/ipfs/go-cid"
	"gorm.io/gorm"
)

// Account returns did's hosting status and head.
func (s *Store) Account(did string) (Account, error) {
	var row accountRow
	if err := s.db.First(&row, "did = ?", did).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Account{}, ErrNotFound
		}
		return Account{}, err
	}
	return toAccount(row)
}

// Accounts lists hosted repos ordered by DID, starting after cursor.
func (s *Store) Accounts(cursor string, limit int) ([]Account, error) {
	var rows []accountRow
	if err := s.db.Where("did > ?", cursor).Order("did asc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		a, err := toAccount(r)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func toAccount(r accountRow) (Account, error) {
	head, err := cid.Decode(r.Head)
	if err != nil {
		return Account{}, err
	}
	data, err := cid.Decode(r.Data)
	if err != nil {
		return Account{}, err
	}
	return Account{Did: r.Did, Rev: r.Rev, Head: head, Data: data, Active: r.Active, Status: r.Status}, nil
}

// RepoCAR exports did's full current state, rooted at its head commit.
func (s *Store) RepoCAR(did string) ([]byte, error) {
	return s.car(did, s.db.Where("did = ?", did))
}

// BlocksCAR returns the requested blocks of did's current state, plus its
// head commit as the CAR root; unknown CIDs are skipped.
func (s *Store) BlocksCAR(did string, cids []string) ([]byte, error) {
	return s.car(did, s.db.Where("did = ? AND cid IN ?", did, cids))
}

// RecordCAR returns an inclusion proof for one record: the head commit, the
// MST nodes, and the record itself (absent when the record is not hosted,
// which proves its absence).
func (s *Store) RecordCAR(did, collection, rkey string) ([]byte, error) {
	q := s.db.Where("did = ? AND kind = ?", did, kindNode)
	if rec, err := s.Record(did, collection, rkey); err == nil {
		q = s.db.Where("did = ? AND (kind = ? OR cid = ?)", did, kindNode, rec.Cid.String())
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return s.car(did, q)
}

func (s *Store) car(did string, q *gorm.DB) ([]byte, error) {
	acct, err := s.Account(did)
	if err != nil {
		return nil, err
	}
	var rows []blockRow
	if err := q.Where("kind <> ?", kindCommit).Order("cid asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	var head blockRow
	if err := s.db.First(&head, "did = ? AND cid = ?", did, acct.Head.String()).Error; err != nil {
		return nil, err
	}
	return encodeCAR(acct.Head, append([]blockRow{head}, rows...))
}

// StoredRecord is a hosted record in both encodings.
type StoredRecord struct {
	Collection string
	Rkey       string
	Cid        cid.Cid
	CBOR       []byte
	Value      map[string]any // atproto data model, JSON-marshalable
}

// Record returns one hosted record.
func (s *Store) Record(did, collection, rkey string) (StoredRecord, error) {
	var row recordRow
	err := s.db.First(&row, "did = ? AND collection = ? AND rkey = ?", did, collection, rkey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return StoredRecord{}, ErrNotFound
	}
	if err != nil {
		return StoredRecord{}, err
	}
	return toStored(row)
}

// Records pages through one collection by rkey: descending by default,
// ascending when reverse is set, starting after cursor.
func (s *Store) Records(did, collection, cursor string, limit int, reverse bool) ([]StoredRecord, error) {
	q := s.db.Where("did = ? AND collection = ?", did, collection)
	if reverse {
		if cursor != "" {
			q = q.Where("rkey > ?", cursor)
		}
		q = q.Order("rkey asc")
	} else {
		if cursor != "" {
			q = q.Where("rkey < ?", cursor)
		}
		q = q.Order("rkey desc")
	}
	var rows []recordRow
	if err := q.Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]StoredRecord, 0, len(rows))
	for _, r := range rows {
		rec, err := toStored(r)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// Collections lists the collections did holds records in.
func (s *Store) Collections(did string) ([]string, error) {
	var out []string
	err := s.db.Model(&recordRow{}).Where("did = ?", did).Distinct("collection").Order("collection asc").Pluck("collection", &out).Error
	return out, err
}

func toStored(r recordRow) (StoredRecord, error) {
	c, err := cid.Decode(r.Cid)
	if err != nil {
		return StoredRecord{}, err
	}
	value, err := atdata.UnmarshalCBOR(r.Value)
	if err != nil {
		return StoredRecord{}, err
	}
	return StoredRecord{Collection: r.Collection, Rkey: r.Rkey, Cid: c, CBOR: r.Value, Value: value}, nil
}

// Frame is one sequenced firehose message, already encoded.
type Frame struct {
	Seq  int64
	Data []byte
}

// FramesAfter returns up to limit frames with seq greater than after.
func (s *Store) FramesAfter(after int64, limit int) ([]Frame, error) {
	var rows []eventRow
	if err := s.db.Where("seq > ?", after).Order("seq asc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Frame, len(rows))
	for i, r := range rows {
		out[i] = Frame{Seq: r.Seq, Data: r.Frame}
	}
	return out, nil
}

// LastSeq returns the newest seq handed out, or 0.
func (s *Store) LastSeq() (int64, error) {
	var last int64
	err := s.db.Model(&eventRow{}).Select("COALESCE(MAX(seq), 0)").Scan(&last).Error
	return last, err
}

// Subscribe returns a channel that receives a value whenever new frames are
// sequenced, and a function to stop receiving.
func (s *Store) Subscribe() (<-chan struct{}, func()) { return s.hub.subscribe() }
