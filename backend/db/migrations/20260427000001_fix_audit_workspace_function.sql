-- The audit trigger function was created before the tenant -> workspace
-- terminology cleanup in some deployed databases. Replacing it in a forward
-- migration repairs those already-applied schemas; editing the original
-- migration is not enough once Atlas has recorded it.
create or replace function record_audit_event() returns trigger language plpgsql as $$
declare
  v_entity_type text := tg_argv[0];
  v_actor uuid := nullif(current_setting('folio.actor_user_id', true), '')::uuid;
  v_workspace uuid;
  v_entity_id uuid;
  v_before jsonb;
  v_after jsonb;
  v_action text;
begin
  if tg_op = 'DELETE' then
    v_action := 'deleted';
    v_workspace := old.workspace_id;
    v_entity_id := old.id;
    v_before := to_jsonb(old);
    v_after := null;
  elsif tg_op = 'UPDATE' then
    v_action := 'updated';
    v_workspace := new.workspace_id;
    v_entity_id := new.id;
    v_before := to_jsonb(old);
    v_after := to_jsonb(new);
  else
    v_action := 'created';
    v_workspace := new.workspace_id;
    v_entity_id := new.id;
    v_before := null;
    v_after := to_jsonb(new);
  end if;

  insert into audit_events (
    id, workspace_id, entity_type, entity_id, action,
    actor_user_id, before_jsonb, after_jsonb, occurred_at
  ) values (
    gen_random_uuid(), v_workspace, v_entity_type, v_entity_id, v_action,
    v_actor, v_before, v_after, now()
  );

  return coalesce(new, old);
end;
$$;
