package repo

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"servicedesk/internal/models"
)

type UserRepo struct{ db *gorm.DB }

func NewUserRepo(db *gorm.DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) GetByUsername(username string) (*models.User, error) {
	var u models.User
	if err := r.db.Where("username = ?", username).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) GetByID(id int64) (*models.User, error) {
	var u models.User
	if err := r.db.First(&u, id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) List() ([]models.User, error) {
	var users []models.User
	err := r.db.Order("username").Find(&users).Error
	return users, err
}

func (r *UserRepo) Create(u *models.User) error {
	return r.db.Create(u).Error
}

// UpsertStatic creates or refreshes a demo user sourced from SERVICEDESK_STATIC_USERS.
func (r *UserRepo) UpsertStatic(username, passwordHash string, role models.Role) error {
	u := models.User{Username: username, PasswordHash: passwordHash, Role: role, Source: "static"}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "username"}},
		DoUpdates: clause.AssignmentColumns([]string{"password_hash", "role", "source"}),
	}).Create(&u).Error
}

func (r *UserRepo) UpdateRole(id int64, role models.Role) error {
	return r.db.Model(&models.User{}).Where("id = ?", id).Update("role", role).Error
}
