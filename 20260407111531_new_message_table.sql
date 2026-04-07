-- +goose Up
CREATE TABLE IF NOT EXISTS messages(
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id BIG NOT NULL,
    text TEXT NOT NULL,
    user_name TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    CONSTRAINT CHK_text_NotEmpty CHECK (text <> ''),
    CONSTRAINT CHK_user_name_NotEmpty CHECK (user_name <> '')
);

-- +goose Down
DROP TABLE IF EXISTS messages;