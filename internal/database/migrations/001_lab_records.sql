CREATE TABLE lab_records (
    key text PRIMARY KEY CHECK (char_length(key) BETWEEN 1 AND 64),
    value text NOT NULL CHECK (char_length(value) <= 512)
);
