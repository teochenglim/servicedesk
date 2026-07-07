package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

// KBArticleRepo backs the Knowledge Base Feedback Loop (DESIGN/08 §8.10).
type KBArticleRepo struct{ db *gorm.DB }

func NewKBArticleRepo(db *gorm.DB) *KBArticleRepo { return &KBArticleRepo{db: db} }

func (r *KBArticleRepo) Create(a *models.KBArticle) error { return r.db.Create(a).Error }
func (r *KBArticleRepo) Update(a *models.KBArticle) error { return r.db.Save(a).Error }
func (r *KBArticleRepo) Delete(id int64) error            { return r.db.Delete(&models.KBArticle{}, id).Error }

func (r *KBArticleRepo) Get(id int64) (*models.KBArticle, error) {
	var a models.KBArticle
	if err := r.db.First(&a, id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *KBArticleRepo) ListByStatus(status models.KBArticleStatus) ([]models.KBArticle, error) {
	var as []models.KBArticle
	err := r.db.Where("status = ?", status).Order("created_at DESC").Find(&as).Error
	return as, err
}

func (r *KBArticleRepo) ListPublished() ([]models.KBArticle, error) {
	return r.ListByStatus(models.KBStatusPublished)
}

func (r *KBArticleRepo) LinkService(articleID, serviceID int64) error {
	m := models.KBArticleService{KBArticleID: articleID, ServiceID: serviceID}
	return r.db.Where(m).FirstOrCreate(&m).Error
}

func (r *KBArticleRepo) UnlinkService(articleID, serviceID int64) error {
	return r.db.Where("kb_article_id = ? AND service_id = ?", articleID, serviceID).Delete(&models.KBArticleService{}).Error
}

func (r *KBArticleRepo) ServicesForArticle(articleID int64) ([]models.Service, error) {
	var ss []models.Service
	err := r.db.Joins("JOIN kb_article_services ON kb_article_services.service_id = services.id").
		Where("kb_article_services.kb_article_id = ?", articleID).
		Order("services.name").Find(&ss).Error
	return ss, err
}
