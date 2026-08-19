package knowledge

import (
	"context"
	"encoding/json"
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"
	"time"
)

// ToggleFollow 切换当前用户对文章的关注状态, 返回关注后的布尔值。
// 关注后文章更新会向该用户推送更新通知(见 NotifyFollowersOnUpdate)。
func (s *SocialStore) ToggleFollow(ctx context.Context, articleID, userID string) (bool, error) {
	var c int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_follows WHERE article_id=$1 AND user_id=$2`, articleID, userID).Scan(&c)
	if c > 0 {
		s.db.ExecContext(ctx, `DELETE FROM knowledge_follows WHERE article_id=$1 AND user_id=$2`, articleID, userID)
		// 取消关注同时清空其更新通知
		s.db.ExecContext(ctx, `DELETE FROM knowledge_follow_updates WHERE article_id=$1 AND user_id=$2`, articleID, userID)
		return false, nil
	}
	s.db.ExecContext(ctx, `INSERT INTO knowledge_follows VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, articleID, userID, time.Now())
	return true, nil
}

// ListFollows 返回用户关注的所有文章 ID(按关注时间倒序)。
func (s *SocialStore) ListFollows(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT article_id FROM knowledge_follows WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListBookmarks 返回用户收藏的所有文章 ID(按收藏时间倒序)。
func (s *SocialStore) ListBookmarks(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT article_id FROM knowledge_bookmarks WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		out = append(out, id)
	}
	return out, rows.Err()
}

// NotifyFollowersOnUpdate 文章更新后, 为每位关注者生成一条未读更新通知。
func (s *SocialStore) NotifyFollowersOnUpdate(ctx context.Context, articleID string) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM knowledge_follows WHERE article_id=$1`, articleID)
	if err != nil {
		return
	}
	defer rows.Close()
	var users []string
	for rows.Next() {
		var u string
		rows.Scan(&u)
		users = append(users, u)
	}
	for _, u := range users {
		s.db.ExecContext(ctx, `INSERT INTO knowledge_follow_updates(article_id,user_id,updated_at,read) VALUES($1,$2,$3,false) ON CONFLICT DO NOTHING`, articleID, u, time.Now())
	}
}

// FollowUpdate 关注文章的更新通知。
type FollowUpdate struct {
	ArticleID string `json:"article_id"`
	UpdatedAt string `json:"updated_at"`
	Read      bool   `json:"read"`
}

// ListFollowUpdates 返回当前用户的关注更新通知(unreadOnly=true 仅未读)。
func (s *SocialStore) ListFollowUpdates(ctx context.Context, userID string, unreadOnly bool) ([]FollowUpdate, error) {
	q := `SELECT article_id, updated_at, read FROM knowledge_follow_updates WHERE user_id=$1`
	if unreadOnly {
		q += ` AND read=false`
	}
	q += ` ORDER BY updated_at DESC LIMIT 100`
	rows, err := s.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FollowUpdate
	for rows.Next() {
		var u FollowUpdate
		rows.Scan(&u.ArticleID, &u.UpdatedAt, &u.Read)
		out = append(out, u)
	}
	return out, rows.Err()
}

// MarkFollowUpdateRead 标记某文章对当前用户的更新通知为已读。
func (s *SocialStore) MarkFollowUpdateRead(ctx context.Context, articleID, userID string) {
	s.db.ExecContext(ctx, `UPDATE knowledge_follow_updates SET read=true WHERE article_id=$1 AND user_id=$2`, articleID, userID)
}

// —— 路由处理 ——

func (s *SocialStore) handleFollow(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	if user == "" {
		user = "admin"
	}
	followed, _ := s.ToggleFollow(r.Context(), r.PathValue("id"), user)
	httputil.WriteJSON(w, 200, map[string]bool{"followed": followed})
}

func (s *SocialStore) handleListFollows(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	if user == "" {
		user = "admin"
	}
	ids, _ := s.ListFollows(r.Context(), user)
	httputil.WriteJSON(w, 200, map[string][]string{"article_ids": ids})
}

func (s *SocialStore) handleListBookmarks(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	if user == "" {
		user = "admin"
	}
	ids, _ := s.ListBookmarks(r.Context(), user)
	httputil.WriteJSON(w, 200, map[string][]string{"article_ids": ids})
}

func (s *SocialStore) handleListFollowUpdates(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	if user == "" {
		user = "admin"
	}
	unreadOnly := r.URL.Query().Get("unread") == "true"
	updates, _ := s.ListFollowUpdates(r.Context(), user, unreadOnly)
	httputil.WriteJSON(w, 200, updates)
}

func (s *SocialStore) handleMarkFollowUpdateRead(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	if user == "" {
		user = "admin"
	}
	var body struct {
		ArticleID string `json:"article_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.ArticleID != "" {
		s.MarkFollowUpdateRead(r.Context(), body.ArticleID, user)
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "ok"})
}
