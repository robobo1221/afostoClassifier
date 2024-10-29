-- This is the sqlite3 migration file for the database --

CREATE TABLE IF NOT EXISTS psqr (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    previousPsqrId INTEGER,         -- The previous psqr id
    perc REAL NOT NULL,             -- The percentile to estimate
    count INTEGER NOT NULL,         -- Number of observations added
    q0 REAL NOT NULL,               -- Marker 0
    q1 REAL,                        -- Marker 1
    q2 REAL,                        -- Marker 2 (current estimate)
    q3 REAL,                        -- Marker 3
    q4 REAL,                        -- Marker 4
    n0 INTEGER,                     -- Count of observations for marker 0
    n1 INTEGER,                     -- Count of observations for marker 1
    n2 INTEGER,                     -- Count of observations for marker 2
    n3 INTEGER,                     -- Count of observations for marker 3
    n4 INTEGER,                     -- Count of observations for marker 4
    np0 REAL,                       -- Desired position for marker 0
    np1 REAL,                       -- Desired position for marker 1
    np2 REAL,                       -- Desired position for marker 2
    np3 REAL,                       -- Desired position for marker 3
    np4 REAL,                       -- Desired position for marker 4
    dn0 REAL,                       -- Increment in desired marker positions for marker 0
    dn1 REAL,                       -- Increment for marker 1
    dn2 REAL,                       -- Increment for marker 2
    dn3 REAL,                       -- Increment for marker 3
    dn4 REAL,                       -- Increment for marker 4,
    FOREIGN KEY (previousPsqrId) REFERENCES psqr(id)
);

CREATE TABLE IF NOT EXISTS connection (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    connectionOrigin TEXT NOT NULL, -- The origin of the connection
    currentPsqr95Id INTEGER NOT NULL,        -- The psqr id
    FOREIGN KEY (connectionOrigin) REFERENCES psqr(id)
);

CREATE INDEX IF NOT EXISTS idx_connectionOrigin ON connection(connectionOrigin);