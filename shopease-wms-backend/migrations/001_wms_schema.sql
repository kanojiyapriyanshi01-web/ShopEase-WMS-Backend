-- ShopEase WMS Database Schema
-- Run: psql $DATABASE_URL -f 001_wms_schema.sql

CREATE SCHEMA IF NOT EXISTS wms;

-- ─────────────────────────────────────────
-- ENUMS
-- ─────────────────────────────────────────

CREATE TYPE wms.warehouse_status AS ENUM ('active', 'inactive', 'maintenance');

CREATE TYPE wms.user_role AS ENUM (
    'super_admin', 'warehouse_manager', 'inventory_staff',
    'picker', 'packer', 'dispatcher', 'qc_inspector'
);

CREATE TYPE wms.order_status AS ENUM (
    'received', 'inventory_reserved', 'picking_assigned',
    'picked', 'packing_assigned', 'packed',
    'ready_to_dispatch', 'shipped', 'delivered', 'cancelled'
);

CREATE TYPE wms.return_condition AS ENUM (
    'good_condition', 'damaged', 'used', 'wrong_product',
    'missing_parts', 'fake_product'
);

CREATE TYPE wms.return_status AS ENUM (
    'requested', 'received', 'qc_pending', 'approved', 'rejected', 'completed'
);

CREATE TYPE wms.transfer_status AS ENUM (
    'requested', 'approved', 'in_transit', 'received', 'completed'
);

CREATE TYPE wms.audit_type AS ENUM ('daily', 'weekly', 'monthly', 'annual', 'spot');

CREATE TYPE wms.alert_severity AS ENUM ('low', 'medium', 'high', 'critical');

CREATE TYPE wms.stock_type AS ENUM (
    'available', 'reserved', 'damaged', 'returned', 'transit', 'replacement'
);

-- ─────────────────────────────────────────
-- WAREHOUSES
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(100) NOT NULL,
    code            VARCHAR(10) UNIQUE NOT NULL,   -- e.g. WH01, WH02
    address         TEXT NOT NULL,
    city            VARCHAR(100) NOT NULL,
    state           VARCHAR(100),
    pincode         VARCHAR(10),
    manager_id      UUID,                           -- FK to wms.warehouse_users
    capacity_sqft   INTEGER DEFAULT 0,
    status          wms.warehouse_status DEFAULT 'active',
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

-- ─────────────────────────────────────────
-- WAREHOUSE USERS (staff)
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    warehouse_id    UUID REFERENCES wms.warehouses(id) ON DELETE SET NULL,
    name            VARCHAR(100) NOT NULL,
    email           VARCHAR(150) UNIQUE NOT NULL,
    phone           VARCHAR(20),
    role            wms.user_role NOT NULL,
    password_hash   TEXT NOT NULL,
    shift           VARCHAR(20) DEFAULT 'morning',  -- morning, afternoon, night
    is_active       BOOLEAN DEFAULT TRUE,
    fcm_token       TEXT,
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

ALTER TABLE wms.warehouses
    ADD CONSTRAINT fk_warehouse_manager
    FOREIGN KEY (manager_id) REFERENCES wms.warehouse_users(id);

-- ─────────────────────────────────────────
-- BIN / RACK MANAGEMENT
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_bins (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    warehouse_id    UUID NOT NULL REFERENCES wms.warehouses(id),
    floor           VARCHAR(10) NOT NULL,   -- A, B, C
    zone            VARCHAR(10) NOT NULL,   -- Z1, Z2
    rack            VARCHAR(10) NOT NULL,   -- R01, R02
    shelf           VARCHAR(10) NOT NULL,   -- S01, S02
    bin_code        VARCHAR(20) NOT NULL,   -- WH01-A-R01-S02-B12
    barcode         TEXT UNIQUE,
    capacity_units  INTEGER DEFAULT 100,
    current_units   INTEGER DEFAULT 0,
    is_active       BOOLEAN DEFAULT TRUE,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(warehouse_id, floor, zone, rack, shelf, bin_code)
);

-- ─────────────────────────────────────────
-- INVENTORY
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_inventory (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    warehouse_id    UUID NOT NULL REFERENCES wms.warehouses(id),
    product_id      UUID NOT NULL,                  -- FK to public.products (ShopEase)
    seller_id       UUID,                           -- FK to public.sellers
    sku             VARCHAR(100) NOT NULL,
    barcode         VARCHAR(100) UNIQUE,
    qr_code         TEXT,
    batch_number    VARCHAR(50),
    bin_id          UUID REFERENCES wms.warehouse_bins(id),
    qty_available   INTEGER NOT NULL DEFAULT 0,
    qty_reserved    INTEGER NOT NULL DEFAULT 0,
    qty_damaged     INTEGER NOT NULL DEFAULT 0,
    qty_returned    INTEGER NOT NULL DEFAULT 0,
    qty_transit     INTEGER NOT NULL DEFAULT 0,
    min_stock_level INTEGER DEFAULT 10,
    cost_price      NUMERIC(12,2),
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(warehouse_id, product_id, batch_number)
);

CREATE INDEX idx_inventory_warehouse ON wms.warehouse_inventory(warehouse_id);
CREATE INDEX idx_inventory_sku ON wms.warehouse_inventory(sku);
CREATE INDEX idx_inventory_barcode ON wms.warehouse_inventory(barcode);

-- ─────────────────────────────────────────
-- GRN — Goods Received Notes (stock inward)
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_grn (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    grn_number      VARCHAR(50) UNIQUE NOT NULL,    -- GRN-20240601-001
    warehouse_id    UUID NOT NULL REFERENCES wms.warehouses(id),
    seller_id       UUID NOT NULL,
    invoice_number  VARCHAR(100),
    invoice_date    DATE,
    batch_number    VARCHAR(50),
    received_by     UUID REFERENCES wms.warehouse_users(id),
    items           JSONB NOT NULL DEFAULT '[]',    -- [{product_id, sku, recv_qty, accepted_qty, rejected_qty, reason}]
    notes           TEXT,
    status          VARCHAR(20) DEFAULT 'completed',
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

-- ─────────────────────────────────────────
-- WAREHOUSE ORDERS
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_orders (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shopease_order_id   UUID NOT NULL UNIQUE,       -- FK to public.orders
    warehouse_id        UUID NOT NULL REFERENCES wms.warehouses(id),
    status              wms.order_status DEFAULT 'received',
    priority            INTEGER DEFAULT 0,           -- 0=normal, 1=urgent, 2=express
    picker_id           UUID REFERENCES wms.warehouse_users(id),
    packer_id           UUID REFERENCES wms.warehouse_users(id),
    dispatcher_id       UUID REFERENCES wms.warehouse_users(id),
    qc_inspector_id     UUID REFERENCES wms.warehouse_users(id),
    picking_started_at  TIMESTAMPTZ,
    picking_done_at     TIMESTAMPTZ,
    packing_done_at     TIMESTAMPTZ,
    shipped_at          TIMESTAMPTZ,
    delivered_at        TIMESTAMPTZ,
    notes               TEXT,
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_worders_status ON wms.warehouse_orders(status);
CREATE INDEX idx_worders_warehouse ON wms.warehouse_orders(warehouse_id);
CREATE INDEX idx_worders_shopease ON wms.warehouse_orders(shopease_order_id);

-- ─────────────────────────────────────────
-- PICKING
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_pickings (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    warehouse_order_id  UUID NOT NULL REFERENCES wms.warehouse_orders(id),
    picker_id           UUID NOT NULL REFERENCES wms.warehouse_users(id),
    picking_list        JSONB NOT NULL DEFAULT '[]', -- [{inventory_id, sku, bin_code, qty_required, qty_picked, scanned_at}]
    status              VARCHAR(20) DEFAULT 'assigned', -- assigned, in_progress, completed
    started_at          TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    notes               TEXT,
    created_at          TIMESTAMPTZ DEFAULT NOW()
);

-- ─────────────────────────────────────────
-- PACKING
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_packings (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    warehouse_order_id  UUID NOT NULL REFERENCES wms.warehouse_orders(id),
    packer_id           UUID NOT NULL REFERENCES wms.warehouse_users(id),
    box_size            VARCHAR(20),                 -- small, medium, large, xl
    weight_kg           NUMERIC(6,2),
    dimensions_cm       JSONB,                       -- {l, w, h}
    invoice_url         TEXT,
    label_url           TEXT,
    status              VARCHAR(20) DEFAULT 'assigned',
    completed_at        TIMESTAMPTZ,
    notes               TEXT,
    created_at          TIMESTAMPTZ DEFAULT NOW()
);

-- ─────────────────────────────────────────
-- SHIPMENTS
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_shipments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    warehouse_order_id  UUID NOT NULL REFERENCES wms.warehouse_orders(id),
    dispatcher_id       UUID REFERENCES wms.warehouse_users(id),
    courier             VARCHAR(50),                 -- delhivery, shiprocket, bluedart
    awb_number          VARCHAR(100) UNIQUE,
    tracking_url        TEXT,
    weight_kg           NUMERIC(6,2),
    box_size            VARCHAR(20),
    courier_response    JSONB,
    shipped_at          TIMESTAMPTZ,
    expected_delivery   DATE,
    actual_delivery     TIMESTAMPTZ,
    status              VARCHAR(30) DEFAULT 'created',
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

-- ─────────────────────────────────────────
-- RETURNS
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_returns (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shopease_return_id  UUID NOT NULL UNIQUE,
    shopease_order_id   UUID NOT NULL,
    warehouse_id        UUID NOT NULL REFERENCES wms.warehouses(id),
    product_id          UUID NOT NULL,
    qty                 INTEGER NOT NULL DEFAULT 1,
    customer_reason     TEXT,
    status              wms.return_status DEFAULT 'requested',
    condition           wms.return_condition,
    qc_inspector_id     UUID REFERENCES wms.warehouse_users(id),
    qc_notes            TEXT,
    qc_images           JSONB DEFAULT '[]',
    resolution          VARCHAR(20),                 -- refund, replacement, rejected
    received_at         TIMESTAMPTZ,
    qc_done_at          TIMESTAMPTZ,
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

-- ─────────────────────────────────────────
-- REPLACEMENTS
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_replacements (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    return_id           UUID NOT NULL REFERENCES wms.warehouse_returns(id),
    warehouse_id        UUID NOT NULL REFERENCES wms.warehouses(id),
    product_id          UUID NOT NULL,
    qty                 INTEGER NOT NULL DEFAULT 1,
    inventory_reserved  BOOLEAN DEFAULT FALSE,
    warehouse_order_id  UUID REFERENCES wms.warehouse_orders(id), -- new order for replacement
    status              VARCHAR(30) DEFAULT 'approved',
    notes               TEXT,
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

-- ─────────────────────────────────────────
-- STOCK TRANSFERS
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_transfers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_warehouse_id   UUID NOT NULL REFERENCES wms.warehouses(id),
    to_warehouse_id     UUID NOT NULL REFERENCES wms.warehouses(id),
    items               JSONB NOT NULL DEFAULT '[]', -- [{inventory_id, qty, sku}]
    status              wms.transfer_status DEFAULT 'requested',
    requested_by        UUID REFERENCES wms.warehouse_users(id),
    approved_by         UUID REFERENCES wms.warehouse_users(id),
    dispatched_at       TIMESTAMPTZ,
    received_at         TIMESTAMPTZ,
    notes               TEXT,
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

-- ─────────────────────────────────────────
-- AUDITS
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_audits (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    warehouse_id        UUID NOT NULL REFERENCES wms.warehouses(id),
    audit_type          wms.audit_type NOT NULL,
    conducted_by        UUID REFERENCES wms.warehouse_users(id),
    audit_items         JSONB NOT NULL DEFAULT '[]', -- [{inventory_id, sku, expected_qty, actual_qty, difference, reason}]
    total_discrepancies INTEGER DEFAULT 0,
    status              VARCHAR(20) DEFAULT 'in_progress', -- in_progress, completed, flagged
    started_at          TIMESTAMPTZ DEFAULT NOW(),
    completed_at        TIMESTAMPTZ,
    notes               TEXT,
    created_at          TIMESTAMPTZ DEFAULT NOW()
);

-- ─────────────────────────────────────────
-- ALERTS
-- ─────────────────────────────────────────

CREATE TABLE wms.warehouse_alerts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    warehouse_id    UUID REFERENCES wms.warehouses(id),
    alert_type      VARCHAR(50) NOT NULL, -- low_stock, out_of_stock, delayed_dispatch, return_pending, audit_failed
    title           VARCHAR(200) NOT NULL,
    message         TEXT,
    severity        wms.alert_severity DEFAULT 'medium',
    reference_id    UUID,                 -- inventory_id, order_id, etc.
    reference_type  VARCHAR(50),
    is_resolved     BOOLEAN DEFAULT FALSE,
    resolved_by     UUID REFERENCES wms.warehouse_users(id),
    resolved_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

-- ─────────────────────────────────────────
-- STAFF ATTENDANCE
-- ─────────────────────────────────────────

CREATE TABLE wms.staff_attendance (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES wms.warehouse_users(id),
    warehouse_id    UUID NOT NULL REFERENCES wms.warehouses(id),
    date            DATE NOT NULL,
    check_in        TIMESTAMPTZ,
    check_out       TIMESTAMPTZ,
    shift           VARCHAR(20),
    status          VARCHAR(20) DEFAULT 'present', -- present, absent, late, half_day
    UNIQUE(user_id, date)
);

-- ─────────────────────────────────────────
-- INVENTORY STOCK MOVEMENTS (audit log)
-- ─────────────────────────────────────────

CREATE TABLE wms.stock_movements (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    warehouse_id    UUID NOT NULL REFERENCES wms.warehouses(id),
    inventory_id    UUID NOT NULL REFERENCES wms.warehouse_inventory(id),
    movement_type   VARCHAR(30) NOT NULL, -- inward, outward, transfer, adjustment, damaged, returned
    quantity        INTEGER NOT NULL,
    reference_id    UUID,
    reference_type  VARCHAR(50),         -- grn, order, transfer, audit
    notes           TEXT,
    created_by      UUID REFERENCES wms.warehouse_users(id),
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_movements_inventory ON wms.stock_movements(inventory_id);
CREATE INDEX idx_movements_warehouse ON wms.stock_movements(warehouse_id);
CREATE INDEX idx_movements_created ON wms.stock_movements(created_at);

-- ─────────────────────────────────────────
-- TRIGGERS: auto-alert on low stock
-- ─────────────────────────────────────────

CREATE OR REPLACE FUNCTION wms.check_low_stock()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.qty_available <= NEW.min_stock_level AND NEW.qty_available > 0 THEN
        INSERT INTO wms.warehouse_alerts (warehouse_id, alert_type, title, message, severity, reference_id, reference_type)
        VALUES (NEW.warehouse_id, 'low_stock', 'Low stock alert',
                'SKU ' || NEW.sku || ' has only ' || NEW.qty_available || ' units left',
                'high', NEW.id, 'inventory')
        ON CONFLICT DO NOTHING;
    END IF;
    IF NEW.qty_available = 0 THEN
        INSERT INTO wms.warehouse_alerts (warehouse_id, alert_type, title, message, severity, reference_id, reference_type)
        VALUES (NEW.warehouse_id, 'out_of_stock', 'Out of stock',
                'SKU ' || NEW.sku || ' is out of stock',
                'critical', NEW.id, 'inventory')
        ON CONFLICT DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_low_stock
    AFTER UPDATE ON wms.warehouse_inventory
    FOR EACH ROW
    WHEN (NEW.qty_available != OLD.qty_available)
    EXECUTE FUNCTION wms.check_low_stock();

-- ─────────────────────────────────────────
-- TRIGGERS: updated_at auto-update
-- ─────────────────────────────────────────

CREATE OR REPLACE FUNCTION wms.update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_warehouses_updated_at BEFORE UPDATE ON wms.warehouses FOR EACH ROW EXECUTE FUNCTION wms.update_updated_at();
CREATE TRIGGER trg_inventory_updated_at BEFORE UPDATE ON wms.warehouse_inventory FOR EACH ROW EXECUTE FUNCTION wms.update_updated_at();
CREATE TRIGGER trg_orders_updated_at BEFORE UPDATE ON wms.warehouse_orders FOR EACH ROW EXECUTE FUNCTION wms.update_updated_at();
CREATE TRIGGER trg_shipments_updated_at BEFORE UPDATE ON wms.warehouse_shipments FOR EACH ROW EXECUTE FUNCTION wms.update_updated_at();
CREATE TRIGGER trg_returns_updated_at BEFORE UPDATE ON wms.warehouse_returns FOR EACH ROW EXECUTE FUNCTION wms.update_updated_at();

-- ─────────────────────────────────────────
-- SEED: default super admin
-- ─────────────────────────────────────────
-- INSERT INTO wms.warehouse_users (name, email, role, password_hash, warehouse_id)
-- VALUES ('Super Admin', 'admin@wms.shopease.com', 'super_admin', '$2a$10$...', NULL);

COMMENT ON SCHEMA wms IS 'ShopEase Warehouse Management System schema';
