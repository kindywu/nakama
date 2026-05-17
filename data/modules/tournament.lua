--[[
 Copyright 2018 The Nakama Authors

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

 http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
--]]

local nk = require("nakama")
local du = require("debug_utils")

--[[
  Test Tournament function calls from client libraries.
--]]

local function tournament_end_callback(_context, tournament, sessionEnd, expiry)
  local records, owner_records, nc, pc = nk.leaderboard_records_list(tournament.id, nil, 1, "", expiry)
  if #records >= 1 then
    local user_id = records[1].owner_id
    local metadata = { won = tournament.id }
    nk.account_update_id(user_id, metadata)
  end
end
nk.register_tournament_end(tournament_end_callback)

local function tournament_reset_callback(_context, tournament, sessionEnd, expiry)
  local records, owner_records, nc, pc = nk.leaderboard_records_list(tournament.id)
  if #records >= 1 then
    local user_id = records[1].owner_id
    local metadata = { expiry_tournament = tournament.id }
    nk.account_update_id(user_id, metadata)
  end
end
nk.register_tournament_reset(tournament_reset_callback)

local function leaderboard_reset_callback(_context, leaderboard, expiry)
  local records, owner_records, nc, pc = nk.leaderboard_records_list(leaderboard.id)
  if #records >= 1 then
    local user_id = records[1].owner_id
    local metadata = { expiry_leaderboard = leaderboard.id }
    nk.account_update_id(user_id, metadata)
  end
end
nk.register_leaderboard_reset(leaderboard_reset_callback)

local function create_same_tournament_multiple_times(_context, payload)
  local args = nk.json_decode(payload)
  local id = nk.uuid_v4()
  nk.tournament_create(id, args.authoritative, args.sort_order, args.operator, args.duration, args.reset_schedule, nil,
    args.title, args.description, args.category, args.start_time, args.end_time, args.max_size, args.max_num_score, args.join_required)

  -- should not throw a new error
  nk.tournament_create(id, args.authoritative, args.sort_order, args.operator, args.duration, args.reset_schedule, nil,
    args.title, args.description, args.category, args.start_time, args.end_time, args.max_size, args.max_num_score, args.join_required)

  local response = {
    tournament_id = id
  }
  return nk.json_encode(response)
end
nk.register_rpc(create_same_tournament_multiple_times, "clientrpc.create_same_tournament_multiple_times")

-- Create a tournament with optional fields defaulted.
--
-- Payload (JSON): all fields optional except title.
--   id               string   (empty = auto-generated UUID)
--   sort_order       string   "asc"|"desc", default "desc"
--   operator         string   "best"|"set"|"incr"|"decr", default "best"
--   duration         number   seconds, must be > 0, default 300
--   reset_schedule   string   cron expression, or "" for one-shot
--   title            string   required
--   description      string   default ""
--   category         number   0-127, default 0
--   start_time       number   Unix timestamp, 0 = immediately
--   end_time         number   Unix timestamp, 0 = never
--   max_size         number   0 = unlimited
--   max_num_score    number   0 = unlimited
--   join_required    bool     default false
--   authoritative    bool     default false
local function create_tournament(context, payload)
  local data = nk.json_decode(payload)
  if not data then
    error("invalid JSON payload")
  end

  local id            = (data.id and data.id ~= "") and data.id or nk.uuid_v4()
  local authoritative = data.authoritative or false
  local sort_order    = data.sort_order or "desc"
  local operator      = data.operator or "best"
  local duration      = tonumber(data.duration) or 300
  local reset_schedule = data.reset_schedule or ""
  local title         = data.title or ""
  local description   = data.description or ""
  local category      = tonumber(data.category) or 0
  local start_time    = tonumber(data.start_time) or 0
  local end_time      = tonumber(data.end_time) or 0
  local max_size      = tonumber(data.max_size) or 0
  local max_num_score = tonumber(data.max_num_score) or 0
  local join_required = data.join_required or false

  if title == "" then
    error("title is required")
  end

  nk.tournament_create(
    id,
    authoritative,
    sort_order,
    operator,
    duration,
    reset_schedule,
    nil,            -- metadata
    title,
    description,
    category,
    start_time,
    end_time,
    max_size,
    max_num_score,
    join_required
  )

  return nk.json_encode({
    tournament_id = id,
    title         = title
  })
end
nk.register_rpc(create_tournament, "clientrpc.create_tournament")

-- Server-authoritative score write for one or many players in a tournament.
-- Called by a dedicated server (with server key) to write tournament
-- records on behalf of players.
--
-- Single-player payload (JSON):
--   tournament_id  string  (required)
--   owner_id       string  (required)  Nakama user_id of the player
--   score          number
--   subscore       number  (optional, default 0)
--   metadata       table   (optional)
--   username       string  (optional, falls back to owner_id)
--   operator       string  (optional, "best"|"set"|"incr"|"decr")
--
-- Batch payload (JSON):
--   tournament_id  string  (required)
--   records        array   (required) each element:
--     owner_id     string  (required)
--     score        number
--     subscore     number  (optional, default 0)
--     metadata     table   (optional)
--     username     string  (optional, falls back to owner_id)
--     operator     string  (optional, "best"|"set"|"incr"|"decr")
local function write_leaderboard_record(context, payload)
  local data = nk.json_decode(payload)
  if not data then
    error("invalid JSON payload")
  end

  local tournament_id = data.tournament_id
  if not tournament_id or tournament_id == "" then
    error("tournament_id is required")
  end

  -- Build the list of records to write.
  -- Supports both single (owner_id) and batch (records array) formats.
  local entries = {}
  if data.records and type(data.records) == "table" then
    for i, r in ipairs(data.records) do
      if not r.owner_id or r.owner_id == "" then
        error("records[" .. i .. "].owner_id is required")
      end
      entries[#entries + 1] = {
        owner_id = r.owner_id,
        score    = tonumber(r.score) or 0,
        subscore = tonumber(r.subscore) or 0,
        metadata = r.metadata or {},
        username = r.username or r.owner_id,
        operator = r.operator or "",
      }
    end
  elseif data.owner_id and data.owner_id ~= "" then
    entries[1] = {
      owner_id = data.owner_id,
      score    = tonumber(data.score) or 0,
      subscore = tonumber(data.subscore) or 0,
      metadata = data.metadata or {},
      username = data.username or data.owner_id,
      operator = data.operator or "",
    }
  else
    error("owner_id or records is required")
  end

  local results = {}
  for _, e in ipairs(entries) do
    local record = nk.tournament_record_write(
      tournament_id,
      e.owner_id,
      e.username,
      e.score,
      e.subscore,
      e.metadata,
      e.operator
    )
    results[#results + 1] = {
      owner_id = e.owner_id,
      rank     = record.rank,
      score    = record.score,
      subscore = record.subscore,
    }
  end

  return nk.json_encode({
    success = true,
    count   = #results,
    records = results,
  })
end
nk.register_rpc(write_leaderboard_record, "clientrpc.write_leaderboard_record")

local function delete_tournament(_context, payload)
  local args = nk.json_decode(payload)

  nk.tournament_delete(args.tournament_id)
end
nk.register_rpc(delete_tournament, "clientrpc.delete_tournament")

local function addattempt_tournament(_context, payload)
  local args = nk.json_decode(payload)

  nk.tournament_add_attempt(args.tournament_id, args.owner_id, args.count)
end
nk.register_rpc(addattempt_tournament, "clientrpc.addattempt_tournament")

