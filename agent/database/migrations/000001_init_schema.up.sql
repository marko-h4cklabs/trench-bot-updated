-- Migration 000001: Initialize core tables (excluding users for now)
CREATE TABLE IF NOT EXISTS buy_bot_data (
    id SERIAL PRIMARY KEY,
    contract TEXT NOT NULL,
    volume FLOAT NOT NULL,
    buys INT NOT NULL,
    sells INT NOT NULL,
    collected_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    verified BOOLEAN DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS filters (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    criteria JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);