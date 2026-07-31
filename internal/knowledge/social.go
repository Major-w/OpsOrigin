package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// SocialStore 知识库社交功能(点赞/评论/打分/收藏/分享)
type SocialStore struct{ db *sql.DB }

func NewSocialStore(db *sql.DB) *SocialStore {
	s := &SocialStore{db: db}
	s.migrate()
	return s
}

func (s *SocialStore) migrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS knowledge_likes (article_id TEXT NOT NULL, user_id TEXT NOT NULL DEFAULT 'admin', created_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY(article_id, user_id))`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS knowledge_bookmarks (article_id TEXT NOT NULL, user_id TEXT NOT NULL DEFAULT 'admin', created_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY(article_id, user_id))`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS knowledge_ratings (id TEXT PRIMARY KEY, article_id TEXT NOT NULL, user_id TEXT NOT NULL DEFAULT 'admin', rating INT NOT NULL CHECK(rating BETWEEN 1 AND 5), comment TEXT DEFAULT '', created_at TIMESTAMPTZ DEFAULT NOW())`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS knowledge_comments (id TEXT PRIMARY KEY, article_id TEXT NOT NULL, user_id TEXT NOT NULL DEFAULT 'admin', content TEXT NOT NULL, parent_id TEXT DEFAULT '', created_at TIMESTAMPTZ DEFAULT NOW())`)
}

type ArticleStats struct {
	Likes    int     `json:"likes"`
	Bookmarks int    `json:"bookmarks"`
	AvgRating float64 `json:"avg_rating"`
	RatingCount int   `json:"rating_count"`
	Liked    bool    `json:"liked"`
	Bookmarked bool  `json:"bookmarked"`
	UserRating *int   `json:"user_rating,omitempty"`
}

type Comment struct {
	ID        string `json:"id"`
	ArticleID string `json:"article_id"`
	UserID    string `json:"user_id"`
	Content   string `json:"content"`
	ParentID  string `json:"parent_id"`
	CreatedAt string `json:"created_at"`
}

type Rating struct {
	ID        string `json:"id"`
	ArticleID string `json:"article_id"`
	UserID    string `json:"user_id"`
	Rating    int    `json:"rating"`
	Comment   string `json:"comment"`
	CreatedAt string `json:"created_at"`
}

func (s *SocialStore) GetStats(ctx context.Context, articleID, userID string) (*ArticleStats, error) {
	st := &ArticleStats{}
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_likes WHERE article_id=$1`, articleID).Scan(&st.Likes)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_bookmarks WHERE article_id=$1`, articleID).Scan(&st.Bookmarks)
	s.db.QueryRowContext(ctx, `SELECT COALESCE(AVG(rating),0), COUNT(*) FROM knowledge_ratings WHERE article_id=$1`, articleID).Scan(&st.AvgRating, &st.RatingCount)
	if userID != "" {
		var liked, bookmarked int
		s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_likes WHERE article_id=$1 AND user_id=$2`, articleID, userID).Scan(&liked)
		s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_bookmarks WHERE article_id=$1 AND user_id=$2`, articleID, userID).Scan(&bookmarked)
		st.Liked = liked > 0
		st.Bookmarked = bookmarked > 0
	}
	return st, nil
}

func (s *SocialStore) ToggleLike(ctx context.Context, articleID, userID string) (bool, error) {
	var c int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_likes WHERE article_id=$1 AND user_id=$2`, articleID, userID).Scan(&c)
	if c > 0 {
		s.db.ExecContext(ctx, `DELETE FROM knowledge_likes WHERE article_id=$1 AND user_id=$2`, articleID, userID)
		return false, nil
	}
	s.db.ExecContext(ctx, `INSERT INTO knowledge_likes VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, articleID, userID, time.Now())
	return true, nil
}

func (s *SocialStore) ToggleBookmark(ctx context.Context, articleID, userID string) (bool, error) {
	var c int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_bookmarks WHERE article_id=$1 AND user_id=$2`, articleID, userID).Scan(&c)
	if c > 0 {
		s.db.ExecContext(ctx, `DELETE FROM knowledge_bookmarks WHERE article_id=$1 AND user_id=$2`, articleID, userID)
		return false, nil
	}
	s.db.ExecContext(ctx, `INSERT INTO knowledge_bookmarks VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, articleID, userID, time.Now())
	return true, nil
}

func (s *SocialStore) Rate(ctx context.Context, articleID, userID string, rating int, comment string) error {
	id := uuid.New().String()
	_, err := s.db.ExecContext(ctx, `INSERT INTO knowledge_ratings(id,article_id,user_id,rating,comment,created_at) VALUES($1,$2,$3,$4,$5,$6)`,
		id, articleID, userID, rating, comment, time.Now())
	return err
}

func (s *SocialStore) AddComment(ctx context.Context, articleID, userID, content, parentID string) (*Comment, error) {
	c := &Comment{ID: uuid.New().String(), ArticleID: articleID, UserID: userID, Content: content, ParentID: parentID, CreatedAt: time.Now().Format(time.RFC3339)}
	_, err := s.db.ExecContext(ctx, `INSERT INTO knowledge_comments(id,article_id,user_id,content,parent_id,created_at) VALUES($1,$2,$3,$4,$5,$6)`,
		c.ID, articleID, userID, content, parentID, time.Now())
	return c, err
}

func (s *SocialStore) ListComments(ctx context.Context, articleID string) ([]Comment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,article_id,user_id,content,COALESCE(parent_id,''),created_at FROM knowledge_comments WHERE article_id=$1 ORDER BY created_at ASC`, articleID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		rows.Scan(&c.ID, &c.ArticleID, &c.UserID, &c.Content, &c.ParentID, &c.CreatedAt)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *SocialStore) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/knowledge/{id}/stats", s.handleStats)
	mux.HandleFunc("POST /api/v1/knowledge/{id}/like", s.handleLike)
	mux.HandleFunc("POST /api/v1/knowledge/{id}/bookmark", s.handleBookmark)
	mux.HandleFunc("POST /api/v1/knowledge/{id}/rate", s.handleRate)
	mux.HandleFunc("POST /api/v1/knowledge/{id}/comments", s.handleAddComment)
	mux.HandleFunc("GET /api/v1/knowledge/{id}/comments", s.handleListComments)
}

func (s *SocialStore) handleStats(w http.ResponseWriter, r *http.Request) {
	st, _ := s.GetStats(r.Context(), r.PathValue("id"), r.URL.Query().Get("user"))
	writeJSON(w, 200, st)
}
func (s *SocialStore) handleLike(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user"); if user == "" { user = "admin" }
	liked, _ := s.ToggleLike(r.Context(), r.PathValue("id"), user)
	writeJSON(w, 200, map[string]bool{"liked": liked})
}
func (s *SocialStore) handleBookmark(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user"); if user == "" { user = "admin" }
	bookmarked, _ := s.ToggleBookmark(r.Context(), r.PathValue("id"), user)
	writeJSON(w, 200, map[string]bool{"bookmarked": bookmarked})
}
func (s *SocialStore) handleRate(w http.ResponseWriter, r *http.Request) {
	var body struct { Rating int `json:"rating"`; Comment string `json:"comment"` }
	json.NewDecoder(r.Body).Decode(&body)
	user := r.URL.Query().Get("user"); if user == "" { user = "admin" }
	s.Rate(r.Context(), r.PathValue("id"), user, body.Rating, body.Comment)
	writeJSON(w, 200, map[string]string{"ok": "rated"})
}
func (s *SocialStore) handleAddComment(w http.ResponseWriter, r *http.Request) {
	var body struct { Content string `json:"content"`; ParentID string `json:"parent_id"` }
	json.NewDecoder(r.Body).Decode(&body)
	user := r.URL.Query().Get("user"); if user == "" { user = "admin" }
	c, _ := s.AddComment(r.Context(), r.PathValue("id"), user, body.Content, body.ParentID)
	writeJSON(w, 201, c)
}
func (s *SocialStore) handleListComments(w http.ResponseWriter, r *http.Request) {
	comments, _ := s.ListComments(r.Context(), r.PathValue("id"))
	writeJSON(w, 200, comments)
}

// writeJSON is defined in service.go (shared)
