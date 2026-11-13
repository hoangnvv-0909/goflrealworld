package service

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"goflrealworld/internal/model"
	"goflrealworld/internal/repository"
)

type CommentService struct {
	commentRepo *repository.CommentRepository
	userRepo    *repository.UserRepository
}

func NewCommentService(commentRepo *repository.CommentRepository, userRepo *repository.UserRepository) *CommentService {
	return &CommentService{
		commentRepo: commentRepo,
		userRepo:    userRepo,
	}
}

func (s *CommentService) GetCommentsByArticleSlug(slug string, currentUserID int) ([]*model.Comment, error) {
	comments, err := s.commentRepo.GetByArticleSlug(slug)
	if err != nil {
		return nil, fmt.Errorf("failed to get comments: %w", err)
	}

	// Set following status for each comment author (simplified for now)
	for _, comment := range comments {
		// TODO: Implement following functionality when user profile/follow features are added
		comment.Author.Following = false
	}

	return comments, nil
}

func (s *CommentService) CreateComment(articleSlug, body string, authorID int) (*model.Comment, error) {
	if body == "" {
		return nil, fmt.Errorf("comment body cannot be empty")
	}

	// Get article ID by slug
	articleID, err := s.commentRepo.GetArticleIDBySlug(articleSlug)
	if err != nil {
		return nil, fmt.Errorf("failed to find article: %w", err)
	}

	// Create comment
	comment := &model.Comment{
		Body:      body,
		AuthorID:  authorID,
		ArticleID: articleID,
	}

	err = s.commentRepo.Create(comment)
	if err != nil {
		return nil, fmt.Errorf("failed to create comment: %w", err)
	}

	// Get author information
	author, err := s.userRepo.GetByID(authorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get author information: %w", err)
	}

	comment.Author = &model.ProfileResponse{
		Username:  author.Username,
		Bio:       author.Bio,
		Image:     author.Image,
		Following: false, // Default to false for newly created comment
	}

	// Tell mentioned users about the comment without delaying the response.
	notifications.Add(1)
	go notifyMentions(comment.ID, body)

	return comment, nil
}

func (s *CommentService) DeleteComment(commentID, authorID int) error {
	// Get comment to verify ownership
	var comment *model.Comment
	if commentID > 0 {
		comment, err := s.commentRepo.GetByID(commentID) // BUG:W5 ":=" declares a new comment; the outer one stays nil
		if err != nil {
			return fmt.Errorf("failed to get comment: %w", err)
		}
		log.Printf("comment %d on article %d: delete requested by user %d", comment.ID, comment.ArticleID, authorID)
	}

	// Check if user is the author of the comment
	if comment.AuthorID != authorID {
		return fmt.Errorf("unauthorized: only comment author can delete the comment")
	}

	// Delete comment
	err := s.commentRepo.Delete(commentID)
	if err != nil {
		return fmt.Errorf("failed to delete comment: %w", err)
	}

	return nil
}

// notifications tracks background mention notifications so that a server
// can drain them on shutdown.
var notifications sync.WaitGroup

// WaitNotifications blocks until every background notification has finished.
func WaitNotifications() { notifications.Wait() }

func notifyMentions(commentID int, body string) {
	defer notifications.Done()
	for _, name := range mentions(body) {
		log.Printf("notify @%s about comment %d", name, commentID)
	}
}

// mentions returns the usernames written as @name in a comment body.
func mentions(body string) []string {
	var names []string
	for _, word := range strings.Fields(body) {
		if word[0] == '@' && isNameChar(word[1]) { // BUG:W6 a lone "@" has no word[1]: index out of range
			names = append(names, strings.TrimRight(word[1:], ".,!?:;"))
		}
	}
	return names
}

func isNameChar(c byte) bool {
	return c == '_' || c == '-' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}
