CREATE INDEX activity_actor_at ON activity(actor_id, at);
CREATE INDEX activity_action_actor_at ON activity(action, actor_id, at);
