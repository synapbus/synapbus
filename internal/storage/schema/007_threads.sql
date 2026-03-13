-- Thread/reply support for messages
-- Adds reply_to column to messages for threaded conversations

ALTER TABLE messages ADD COLUMN reply_to INTEGER REFERENCES messages(id);
CREATE INDEX idx_messages_reply_to ON messages(reply_to);
