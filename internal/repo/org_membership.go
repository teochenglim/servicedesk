package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

// OrgMembershipRepo tracks which organizations a (Customer) user can log
// into; a username can belong to 1+ orgs (see models.OrgMembership).
type OrgMembershipRepo struct{ db *gorm.DB }

func NewOrgMembershipRepo(db *gorm.DB) *OrgMembershipRepo { return &OrgMembershipRepo{db: db} }

func (r *OrgMembershipRepo) IsMember(orgID, userID int64) (bool, error) {
	var count int64
	err := r.db.Model(&models.OrgMembership{}).
		Where("org_id = ? AND user_id = ?", orgID, userID).Count(&count).Error
	return count > 0, err
}

func (r *OrgMembershipRepo) Add(orgID, userID int64) error {
	m := models.OrgMembership{OrgID: orgID, UserID: userID}
	return r.db.Where(m).FirstOrCreate(&m).Error
}

func (r *OrgMembershipRepo) Remove(orgID, userID int64) error {
	return r.db.Where("org_id = ? AND user_id = ?", orgID, userID).Delete(&models.OrgMembership{}).Error
}

func (r *OrgMembershipRepo) ListMembers(orgID int64) ([]models.User, error) {
	var users []models.User
	err := r.db.Joins("JOIN org_memberships ON org_memberships.user_id = users.id").
		Where("org_memberships.org_id = ?", orgID).
		Order("users.username").Find(&users).Error
	return users, err
}

func (r *OrgMembershipRepo) ListOrgsForUser(userID int64) ([]models.Organization, error) {
	var orgs []models.Organization
	err := r.db.Joins("JOIN org_memberships ON org_memberships.org_id = organizations.id").
		Where("org_memberships.user_id = ?", userID).
		Order("organizations.name").Find(&orgs).Error
	return orgs, err
}
