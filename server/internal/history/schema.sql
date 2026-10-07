-- The history database of one company: what its plants did, hour by hour.
-- Written by SmartFactory, read by the advisor (DataIntelligence) through model.yaml.
-- Times are local wall-clock time (Asia/Shanghai) without a zone; every bucket is a local hour.

CREATE TABLE IF NOT EXISTS plants (
  plant_id text PRIMARY KEY,
  name     text NOT NULL,
  hall     text NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS parts (
  part_id   text PRIMARY KEY,
  part_no   text NOT NULL DEFAULT '',
  name      text NOT NULL,
  material  text NOT NULL DEFAULT '',
  weight_kg double precision,
  input_kg  double precision,
  customer  text NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS lines (
  line_id       text PRIMARY KEY,
  plant_id      text NOT NULL REFERENCES plants,
  name          text NOT NULL,
  process       text NOT NULL DEFAULT '',
  part_id       text NOT NULL REFERENCES parts,
  ideal_cycle_s double precision NOT NULL,
  order_qty     integer NOT NULL
);

CREATE TABLE IF NOT EXISTS machines (
  machine_id text PRIMARY KEY,
  line_id    text NOT NULL REFERENCES lines,
  plant_id   text NOT NULL REFERENCES plants,
  code       text NOT NULL,
  label      text NOT NULL,
  name       text NOT NULL,
  kind       text NOT NULL,
  seq        integer NOT NULL,
  cycle_s    double precision NOT NULL,
  tool       text,
  tool_life  integer,
  feature    text,
  unit       text
);

-- one row per local hour that has data; shifts: 夜班 00–08, 早班 08–16, 中班 16–24
CREATE TABLE IF NOT EXISTS calendar (
  hour  timestamp PRIMARY KEY,
  day   date NOT NULL,
  shift text NOT NULL
);

CREATE TABLE IF NOT EXISTS machine_hours (
  machine_id   text NOT NULL REFERENCES machines,
  hour         timestamp NOT NULL REFERENCES calendar,
  run_s        double precision NOT NULL,
  idle_s       double precision NOT NULL,
  blocked_s    double precision NOT NULL,
  fault_s      double precision NOT NULL,
  maint_s      double precision NOT NULL,
  changeover_s double precision NOT NULL,
  stopped_s    double precision NOT NULL,
  cycles       integer NOT NULL,
  scrap        integer NOT NULL,
  rework       integer NOT NULL,
  energy_kwh   double precision NOT NULL,
  fuel         double precision NOT NULL,
  PRIMARY KEY (machine_id, hour)
);

CREATE TABLE IF NOT EXISTS line_hours (
  line_id       text NOT NULL REFERENCES lines,
  hour          timestamp NOT NULL REFERENCES calendar,
  planned_s     double precision NOT NULL,
  run_s         double precision NOT NULL,
  started       integer NOT NULL,
  produced      integer NOT NULL,
  good          integer NOT NULL,
  rework        integer NOT NULL,
  scrap         integer NOT NULL,
  good_kg       double precision NOT NULL,
  input_kg      double precision NOT NULL,
  ideal_cycle_s double precision NOT NULL,
  PRIMARY KEY (line_id, hour)
);

CREATE TABLE IF NOT EXISTS quality_hours (
  machine_id  text NOT NULL REFERENCES machines,
  hour        timestamp NOT NULL REFERENCES calendar,
  feature     text NOT NULL,
  unit        text NOT NULL,
  n           integer NOT NULL,
  mean        double precision NOT NULL,
  sd          double precision NOT NULL,
  min         double precision NOT NULL,
  max         double precision NOT NULL,
  lsl         double precision,
  usl         double precision,
  out_of_spec integer NOT NULL,
  PRIMARY KEY (machine_id, hour, feature)
);

CREATE TABLE IF NOT EXISTS stock_hours (
  plant_id  text NOT NULL REFERENCES plants,
  item      text NOT NULL,
  label     text NOT NULL,
  hour      timestamp NOT NULL REFERENCES calendar,
  min_avail integer NOT NULL,
  end_avail integer NOT NULL,
  capacity  integer NOT NULL,
  PRIMARY KEY (plant_id, item, hour)
);

CREATE TABLE IF NOT EXISTS downtime (
  id         bigserial PRIMARY KEY,
  machine_id text NOT NULL REFERENCES machines,
  line_id    text NOT NULL REFERENCES lines,
  hour       timestamp NOT NULL REFERENCES calendar,
  started_at timestamp NOT NULL,
  ended_at   timestamp NOT NULL,
  minutes    double precision NOT NULL,
  category   text NOT NULL,
  reason     text NOT NULL,
  UNIQUE (machine_id, started_at, category)
);

CREATE TABLE IF NOT EXISTS tool_changes (
  id         bigserial PRIMARY KEY,
  machine_id text NOT NULL REFERENCES machines,
  line_id    text NOT NULL REFERENCES lines,
  hour       timestamp NOT NULL REFERENCES calendar,
  started_at timestamp NOT NULL,
  ended_at   timestamp NOT NULL,
  minutes    double precision NOT NULL,
  tool       text NOT NULL,
  hits       integer NOT NULL,
  life       integer NOT NULL,
  limit_pct  double precision NOT NULL,
  trigger    text NOT NULL,
  UNIQUE (machine_id, started_at)
);

CREATE TABLE IF NOT EXISTS defects (
  id          bigserial PRIMARY KEY,
  machine_id  text NOT NULL REFERENCES machines,
  line_id     text NOT NULL REFERENCES lines,
  hour        timestamp NOT NULL REFERENCES calendar,
  at          timestamp NOT NULL,
  defect_type text NOT NULL,
  feature     text NOT NULL,
  value       double precision,
  disposition text NOT NULL,
  order_no    text NOT NULL
);

CREATE TABLE IF NOT EXISTS work_orders (
  order_no     text PRIMARY KEY,
  line_id      text NOT NULL REFERENCES lines,
  part_id      text NOT NULL REFERENCES parts,
  customer     text NOT NULL,
  heat_no      text NOT NULL,
  qty          integer NOT NULL,
  good         integer NOT NULL,
  scrap        integer NOT NULL,
  issued_at    timestamp NOT NULL,
  due_at       timestamp NOT NULL,
  completed_at timestamp,
  -- the hour it was completed (or issued, while open): what "this week's orders" counts by
  hour         timestamp NOT NULL REFERENCES calendar,
  status       text NOT NULL,
  completed    integer NOT NULL,
  on_time      integer NOT NULL
);

-- one material lot (steel heat) as used on a line: what came out of it
CREATE TABLE IF NOT EXISTS lots (
  heat_no    text PRIMARY KEY,
  supplier   text NOT NULL,
  line_id    text NOT NULL REFERENCES lines,
  part_id    text NOT NULL REFERENCES parts,
  first_at   timestamp NOT NULL,
  last_at    timestamp NOT NULL,
  -- the hour it was first used: what "this month's lots" counts by
  hour       timestamp NOT NULL REFERENCES calendar,
  produced   integer NOT NULL,
  good       integer NOT NULL,
  rework     integer NOT NULL,
  scrap      integer NOT NULL
);

ALTER TABLE defects ADD COLUMN IF NOT EXISTS heat_no text NOT NULL DEFAULT '';
ALTER TABLE defects ADD COLUMN IF NOT EXISTS supplier text NOT NULL DEFAULT '';
ALTER TABLE work_orders ADD COLUMN IF NOT EXISTS supplier text NOT NULL DEFAULT '';
ALTER TABLE work_orders ADD COLUMN IF NOT EXISTS part_name text NOT NULL DEFAULT '';
ALTER TABLE lots ADD COLUMN IF NOT EXISTS part_name text NOT NULL DEFAULT '';
-- a family (the line's product) and each of its part numbers are rows here; family names the family
ALTER TABLE parts ADD COLUMN IF NOT EXISTS family text NOT NULL DEFAULT '';
-- units the line's bottleneck machine finished in the hour (what performance is counted on)
ALTER TABLE line_hours ADD COLUMN IF NOT EXISTS neck_cycles integer NOT NULL DEFAULT 0;

-- ---- the business around the plant: customers, shipments, complaints, purchasing, costs ----

CREATE TABLE IF NOT EXISTS customers (
  name      text PRIMARY KEY,
  transit_h double precision NOT NULL DEFAULT 24
);

ALTER TABLE parts ADD COLUMN IF NOT EXISTS price double precision;
ALTER TABLE machine_hours ADD COLUMN IF NOT EXISTS power_cost double precision NOT NULL DEFAULT 0;
ALTER TABLE machine_hours ADD COLUMN IF NOT EXISTS fuel_cost double precision NOT NULL DEFAULT 0;
ALTER TABLE tool_changes ADD COLUMN IF NOT EXISTS cost double precision NOT NULL DEFAULT 0;
ALTER TABLE work_orders ADD COLUMN IF NOT EXISTS price double precision NOT NULL DEFAULT 0;
ALTER TABLE work_orders ADD COLUMN IF NOT EXISTS promise_at timestamp;

-- one steel heat bought: ordered, promised, received, inspected on arrival
CREATE TABLE IF NOT EXISTS purchases (
  po_no       text PRIMARY KEY,
  heat_no     text NOT NULL,
  supplier    text NOT NULL,
  material    text NOT NULL,
  line_id     text NOT NULL REFERENCES lines,
  part_id     text NOT NULL REFERENCES parts,
  tonnes      double precision NOT NULL,
  price       double precision NOT NULL,
  amount      double precision NOT NULL,
  ordered_at  timestamp NOT NULL,
  promised_at timestamp NOT NULL,
  received_at timestamp NOT NULL,
  -- the hour it arrived
  hour        timestamp NOT NULL REFERENCES calendar,
  late        integer NOT NULL,
  result      text NOT NULL,
  reason      text NOT NULL
);

-- one work order's goods on the road to the customer
CREATE TABLE IF NOT EXISTS shipments (
  shipment_no  text PRIMARY KEY,
  order_no     text NOT NULL,
  line_id      text NOT NULL REFERENCES lines,
  customer     text NOT NULL,
  part_id      text NOT NULL REFERENCES parts,
  part_name    text NOT NULL,
  units        integer NOT NULL,
  tonnes       double precision NOT NULL,
  trucks       integer NOT NULL,
  carrier      text NOT NULL,
  departed_at  timestamp NOT NULL,
  eta_at       timestamp NOT NULL,
  promise_at   timestamp NOT NULL,
  delivered_at timestamp,
  -- the hour it left
  hour         timestamp NOT NULL REFERENCES calendar,
  revenue      double precision NOT NULL,
  transit_h    double precision,
  delivered    integer NOT NULL,
  on_time      integer NOT NULL,
  status       text NOT NULL
);

-- a customer finding flawed parts that got past final inspection
CREATE TABLE IF NOT EXISTS complaints (
  complaint_no text PRIMARY KEY,
  customer     text NOT NULL,
  order_no     text NOT NULL,
  shipment_no  text NOT NULL,
  line_id      text NOT NULL REFERENCES lines,
  part_id      text NOT NULL REFERENCES parts,
  part_name    text NOT NULL,
  defect_type  text NOT NULL,
  qty          integer NOT NULL,
  opened_at    timestamp NOT NULL,
  closed_at    timestamp,
  -- the hour it was opened
  hour         timestamp NOT NULL REFERENCES calendar,
  status       text NOT NULL
);

CREATE INDEX IF NOT EXISTS machine_hours_hour ON machine_hours (hour);
CREATE INDEX IF NOT EXISTS line_hours_hour ON line_hours (hour);
CREATE INDEX IF NOT EXISTS downtime_hour ON downtime (hour);
CREATE INDEX IF NOT EXISTS defects_hour ON defects (hour);
