-- +goose Up
CREATE TABLE IF NOT EXISTS read_receipts(
    message_id INTEGER NOT NULL,
    user_name TEXT NOT NULL,
    read_at TIMESTAMP NOT NULL,
    PRIMARY KEY (message_id, user_name),
    FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE,
    CONSTRAINT CHK_user_name_NotEmpty CHECK (user_name <> '')
);

-- +goose Down
DROP TABLE IF EXISTS read_receipts;
