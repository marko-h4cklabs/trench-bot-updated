-- Migration 000001: Revert init_schema
DROP TABLE IF EXISTS filters;
DROP TABLE IF EXISTS buy_bot_data;