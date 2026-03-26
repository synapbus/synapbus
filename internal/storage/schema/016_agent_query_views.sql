-- 016: Agent SQL query views
-- These views are used by the 'query' action to give agents read access
-- to messages they can see. The views expose a stable schema that agents
-- can query via SQL. Access control is enforced at the Go layer by
-- rewriting queries to filter by agent name.

-- Note: SQLite views cannot be parameterized. The Go query executor
-- wraps agent queries in a CTE that filters by the authenticated agent's
-- access (own DMs + joined channels). These views provide the base schema.

-- my_messages: All messages accessible to the calling agent
CREATE VIEW IF NOT EXISTS v_agent_messages AS
SELECT
    m.id,
    m.body,
    m.from_agent,
    m.to_agent,
    m.priority,
    m.status,
    m.metadata,
    m.created_at,
    m.updated_at,
    c.name AS channel_name,
    m.channel_id,
    m.reply_to,
    m.conversation_id
FROM messages m
LEFT JOIN channels c ON c.id = m.channel_id;

-- my_channels: Channels the calling agent has joined
CREATE VIEW IF NOT EXISTS v_agent_channels AS
SELECT
    c.id,
    c.name,
    c.description,
    c.type,
    c.topic,
    c.is_private,
    c.created_at,
    cm.joined_at AS member_since
FROM channels c
JOIN channel_members cm ON cm.channel_id = c.id;

-- channel_messages: Messages in channels (filtered by membership at Go layer)
CREATE VIEW IF NOT EXISTS v_channel_messages AS
SELECT
    m.id,
    m.body,
    m.from_agent,
    m.priority,
    m.status,
    m.metadata,
    m.created_at,
    c.name AS channel_name,
    m.channel_id,
    m.reply_to
FROM messages m
JOIN channels c ON c.id = m.channel_id;
