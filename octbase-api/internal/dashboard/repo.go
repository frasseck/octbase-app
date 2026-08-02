package dashboard

import (
	"database/sql"
	"errors"
)

// Repo handles user_preferences persistence.
type Repo struct{ db *sql.DB }

// NewRepo creates a new dashboard Repo.
func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

// GetPreferences returns a user's preferences, creating a default row on
// first read so a missing row is never a special case for callers.
func (r *Repo) GetPreferences(userID string) (*Preferences, error) {
	p := &Preferences{UserID: userID}
	err := r.db.QueryRow(
		`SELECT language, theme, terminology FROM user_preferences WHERE user_id = $1`, userID,
	).Scan(&p.Language, &p.Theme, &p.Terminology)
	if errors.Is(err, sql.ErrNoRows) {
		p.Language = DefaultLanguage
		p.Theme = DefaultTheme
		p.Terminology = DefaultTerminology
		if _, err := r.db.Exec(
			`INSERT INTO user_preferences (user_id, language, theme, terminology) VALUES ($1,$2,$3,$4)
			 ON CONFLICT (user_id) DO NOTHING`,
			userID, p.Language, p.Theme, p.Terminology,
		); err != nil {
			return nil, err
		}
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// UpsertPreferences saves a user's preferences.
func (r *Repo) UpsertPreferences(p *Preferences) error {
	_, err := r.db.Exec(`
		INSERT INTO user_preferences (user_id, language, theme, terminology, updated_at) VALUES ($1,$2,$3,$4,now())
		ON CONFLICT (user_id) DO UPDATE SET language = EXCLUDED.language, theme = EXCLUDED.theme,
		                                    terminology = EXCLUDED.terminology, updated_at = now()`,
		p.UserID, p.Language, p.Theme, p.Terminology,
	)
	return err
}
