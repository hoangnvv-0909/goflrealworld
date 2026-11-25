package service

import (
	"fmt"
	"sync"

	"goflrealworld/internal/repository"
	"goflrealworld/internal/utils"
)

// TagService handles tag business logic
type TagService struct {
	tagRepo *repository.TagRepository

	mu      sync.Mutex
	popular map[int][]string // GetPopularTags results by limit
}

// NewTagService creates a new tag service
func NewTagService(tagRepo *repository.TagRepository) *TagService {
	return &TagService{
		tagRepo: tagRepo,
		popular: make(map[int][]string),
	}
}

// GetPopularTags retrieves popular tags ordered by usage count
func (s *TagService) GetPopularTags(limit int) ([]string, error) {
	if limit <= 0 {
		limit = 20 // Default limit
	}
	if limit > 100 {
		limit = 100 // Maximum limit
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if tags, ok := s.popular[limit]; ok {
		return tags, nil
	}

	tags, err := s.tagRepo.GetPopularTags(limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get popular tags: %w", err)
	}

	s.popular[limit] = tags
	return tags, nil
}

// GetAllTags retrieves all unique tags
func (s *TagService) GetAllTags() ([]string, error) {
	tags, err := s.tagRepo.GetAllTags()
	if err != nil {
		return nil, fmt.Errorf("failed to get all tags: %w", err)
	}

	return tags, nil
}

// CreateTagsForArticle creates and associates tags with an article
func (s *TagService) CreateTagsForArticle(articleID int, tagNames []string) error {
	// Normalize tags
	normalizedTags := utils.NormalizeTags(tagNames)

	// Validate each tag
	var validTags []string
	for _, tag := range normalizedTags {
		if utils.ValidateTag(tag) {
			sanitized := utils.SanitizeTag(tag)
			if sanitized != "" {
				validTags = append(validTags, sanitized)
			}
		}
	}

	if len(validTags) == 0 {
		return nil // No valid tags to create
	}

	err := s.tagRepo.CreateTagsForArticle(articleID, validTags)
	if err != nil {
		return fmt.Errorf("failed to create tags for article: %w", err)
	}
	s.invalidatePopular()

	return nil
}

// UpdateTagsForArticle updates tags associated with an article
func (s *TagService) UpdateTagsForArticle(articleID int, tagNames []string) error {
	// Normalize tags
	normalizedTags := utils.NormalizeTags(tagNames)

	// Validate each tag
	var validTags []string
	for _, tag := range normalizedTags {
		if utils.ValidateTag(tag) {
			sanitized := utils.SanitizeTag(tag)
			if sanitized != "" {
				validTags = append(validTags, sanitized)
			}
		}
	}

	err := s.tagRepo.UpdateTagsForArticle(articleID, validTags)
	if err != nil {
		return fmt.Errorf("failed to update tags for article: %w", err)
	}
	s.invalidatePopular()

	return nil
}

// GetTagsForArticle retrieves all tags for a specific article
func (s *TagService) GetTagsForArticle(articleID int) ([]string, error) {
	tags, err := s.tagRepo.GetTagsForArticle(articleID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tags for article: %w", err)
	}

	return tags, nil
}

// GetArticleCountByTag gets the number of articles for a specific tag
func (s *TagService) GetArticleCountByTag(tagName string) (int, error) {
	count, err := s.tagRepo.GetArticleCountByTag(tagName)
	if err != nil {
		return 0, fmt.Errorf("failed to get article count for tag: %w", err)
	}

	return count, nil
}

// DeleteUnusedTags removes tags that are not associated with any articles
func (s *TagService) DeleteUnusedTags() error {
	err := s.tagRepo.DeleteUnusedTags()
	if err != nil {
		return fmt.Errorf("failed to delete unused tags: %w", err)
	}

	return nil
}

// invalidatePopular drops cached popular tags after tag usage changes.
func (s *TagService) invalidatePopular() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.popular = nil // BUG:W4 should be clear(s.popular); the next GetPopularTags writes to a nil map
}
