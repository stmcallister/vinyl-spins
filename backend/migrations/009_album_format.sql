-- +goose Up
alter table albums add column if not exists format text;
alter table albums add column if not exists original_year int;

-- +goose Down
alter table albums drop column if exists format;
alter table albums drop column if exists original_year;
