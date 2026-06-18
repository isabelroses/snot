package state

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type knotMemberRow struct {
	Subject string `gorm:"column:subject;primaryKey"`
	AddedBy string `gorm:"column:added_by"`
	Created string `gorm:"column:created"`
}

func (knotMemberRow) TableName() string { return "knot_members" }

type repoCollaboratorRow struct {
	Repo    string `gorm:"column:repo;primaryKey"`
	Subject string `gorm:"column:subject;primaryKey"`
	AddedBy string `gorm:"column:added_by"`
	Created string `gorm:"column:created"`
}

func (repoCollaboratorRow) TableName() string { return "repo_collaborators" }

// Member is a knot member (a DID allowed to own/create repos on this knot).
type Member struct {
	Subject string
	AddedBy string
	Created string
}

// Collaborator is a per-repo collaborator DID.
type Collaborator struct {
	Subject string
	AddedBy string
	Created string
}

// ACL is the knot's member/collaborator store. It governs appview-side
// permissions only; snot does not gate git pushes.
type ACL struct {
	db *gorm.DB
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// SeedMembers idempotently ensures each DID is a member (AddedBy = itself).
func (a *ACL) SeedMembers(dids []string) error {
	if len(dids) == 0 {
		return nil
	}
	now := nowRFC3339()
	rows := make([]knotMemberRow, 0, len(dids))
	for _, did := range dids {
		rows = append(rows, knotMemberRow{Subject: did, AddedBy: did, Created: now})
	}
	return a.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

func (a *ACL) Members() ([]Member, error) {
	var rows []knotMemberRow
	if err := a.db.Order("created asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Member, len(rows))
	for i, r := range rows {
		out[i] = Member{Subject: r.Subject, AddedBy: r.AddedBy, Created: r.Created}
	}
	return out, nil
}

func (a *ACL) IsMember(did string) (bool, error) {
	var n int64
	if err := a.db.Model(&knotMemberRow{}).Where("subject = ?", did).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

func (a *ACL) AddMember(subject, addedBy string) error {
	row := knotMemberRow{Subject: subject, AddedBy: addedBy, Created: nowRFC3339()}
	return a.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

func (a *ACL) RemoveMember(subject string) error {
	return a.db.Where("subject = ?", subject).Delete(&knotMemberRow{}).Error
}

func (a *ACL) Collaborators(repoDid string) ([]Collaborator, error) {
	var rows []repoCollaboratorRow
	if err := a.db.Where("repo = ?", repoDid).Order("created asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Collaborator, len(rows))
	for i, r := range rows {
		out[i] = Collaborator{Subject: r.Subject, AddedBy: r.AddedBy, Created: r.Created}
	}
	return out, nil
}

func (a *ACL) AddCollaborator(repoDid, subject, addedBy string) error {
	row := repoCollaboratorRow{Repo: repoDid, Subject: subject, AddedBy: addedBy, Created: nowRFC3339()}
	return a.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

func (a *ACL) RemoveCollaborator(repoDid, subject string) error {
	return a.db.Where("repo = ? AND subject = ?", repoDid, subject).Delete(&repoCollaboratorRow{}).Error
}
