-- Demo data for the example reports in examples/reports.
--
-- Load it into an empty MySQL 8 or MariaDB 10.6+ database:
--
--   mysql -u root -p -e "CREATE DATABASE reports_demo CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
--   mysql -u root -p reports_demo < examples/demo-schema.sql
--
-- then give the server an account that can only read it (DOCFIND_DB_*, see
-- .env.example):
--
--   CREATE USER 'reports_ro'@'%' IDENTIFIED BY 'choose-a-password';
--   GRANT SELECT ON reports_demo.* TO 'reports_ro'@'%';
--
-- Every name, company and figure here is invented.

CREATE TABLE customers (
  id   INT PRIMARY KEY,
  name VARCHAR(120) NOT NULL,
  city VARCHAR(60)  NOT NULL
) DEFAULT CHARSET=utf8mb4;

-- People as some systems store them: the job title and team after the name,
-- so one person appears once per role. The reports fold them back to one.
CREATE TABLE staff (
  id        INT PRIMARY KEY,
  full_name VARCHAR(160) NOT NULL
) DEFAULT CHARSET=utf8mb4;

CREATE TABLE orders (
  id           INT PRIMARY KEY,
  order_no     VARCHAR(20)   NOT NULL,
  order_type   TINYINT       NOT NULL, -- 1 domestic, 2 export
  customer_id  INT           NOT NULL,
  owner_name   VARCHAR(160)  NOT NULL, -- written like staff.full_name
  status       VARCHAR(20)   NOT NULL, -- open, shipped, cancelled
  submitted_on CHAR(10)      NOT NULL, -- a Jalali date as text: 1405/03/12
  amount       DECIMAL(18,2) NOT NULL
) DEFAULT CHARSET=utf8mb4;

-- An order's line items: the detail a builder report opens under one order.
CREATE TABLE order_lines (
  id         INT PRIMARY KEY,
  order_id   INT           NOT NULL,
  item       VARCHAR(120)  NOT NULL,
  qty        INT           NOT NULL,
  unit_price DECIMAL(18,2) NOT NULL
) DEFAULT CHARSET=utf8mb4;

CREATE TABLE stock (
  id         INT PRIMARY KEY,
  warehouse  VARCHAR(20)  NOT NULL, -- central, branch
  item       VARCHAR(120) NOT NULL,
  qty        INT          NOT NULL,
  updated_at DATE         NOT NULL
) DEFAULT CHARSET=utf8mb4;

INSERT INTO customers VALUES
  (1, 'بازرگانی آلفا', 'تهران'),
  (2, 'صنایع بتا', 'اصفهان'),
  (3, 'پخش گاما', 'تبریز'),
  (4, 'تجارت دلتا', 'شیراز'),
  (5, 'شرکت نمونهٔ شرق', 'مشهد');

INSERT INTO staff VALUES
  (1, 'زهرا مرادی کارشناس فروش 1-2'),
  (2, 'زهرا مرادی سرپرست تیم فروش 3'),
  (3, 'تینا امینی کارشناس فروش 1-4'),
  (4, 'آرش رحیمی کارشناس فروش 2-1');

INSERT INTO orders VALUES
  (1,  'ORD-1001', 1, 1, 'زهرا مرادی کارشناس فروش 1-2',   'shipped',   '1404/10/04', 1250000000.00),
  (2,  'ORD-1002', 2, 2, 'تینا امینی کارشناس فروش 1-4',   'shipped',   '1404/10/19',  830000000.00),
  (3,  'ORD-1003', 1, 3, 'آرش رحیمی کارشناس فروش 2-1',    'cancelled', '1404/11/02',  145000000.00),
  (4,  'ORD-1004', 1, 1, 'زهرا مرادی کارشناس فروش 1-2',   'shipped',   '1404/11/27',  990000000.00),
  (5,  'ORD-1005', 2, 4, 'تینا امینی کارشناس فروش 1-4',   'shipped',   '1404/12/09', 2100000000.00),
  (6,  'ORD-1006', 1, 5, 'آرش رحیمی کارشناس فروش 2-1',    'shipped',   '1405/01/18',  420000000.00),
  (7,  'ORD-1007', 1, 2, 'زهرا مرادی سرپرست تیم فروش 3',  'shipped',   '1405/02/03', 1675000000.00),
  (8,  'ORD-1008', 2, 1, 'تینا امینی کارشناس فروش 1-4',   'shipped',   '1405/02/21',  760000000.00),
  (9,  'ORD-1009', 1, 3, 'آرش رحیمی کارشناس فروش 2-1',    'shipped',   '1405/03/08',  310000000.00),
  (10, 'ORD-1010', 1, 4, 'زهرا مرادی سرپرست تیم فروش 3',  'cancelled', '1405/03/30',  540000000.00),
  (11, 'ORD-1011', 2, 5, 'تینا امینی کارشناس فروش 1-4',   'shipped',   '1405/04/12', 1890000000.00),
  (12, 'ORD-1012', 1, 1, 'آرش رحیمی کارشناس فروش 2-1',    'shipped',   '1405/04/29',  275000000.00),
  (13, 'ORD-1013', 1, 2, 'زهرا مرادی کارشناس فروش 1-2',   'shipped',   '1405/05/14', 1120000000.00),
  (14, 'ORD-1014', 2, 3, 'تینا امینی کارشناس فروش 1-4',   'shipped',   '1405/05/31',  640000000.00),
  (15, 'ORD-1015', 1, 4, 'زهرا مرادی سرپرست تیم فروش 3',  'shipped',   '1405/06/16',  985000000.00),
  (16, 'ORD-1016', 1, 5, 'آرش رحیمی کارشناس فروش 2-1',    'open',      '1405/07/02',  455000000.00),
  (17, 'ORD-1017', 2, 1, 'تینا امینی کارشناس فروش 1-4',   'open',      '1405/07/11', 1340000000.00),
  (18, 'ORD-1018', 1, 2, 'زهرا مرادی کارشناس فروش 1-2',   'open',      '1405/07/20',  720000000.00);

INSERT INTO stock VALUES
  (1,  'central', 'کاغذ A4 (بسته)',   480, '2026-04-12'),
  (2,  'central', 'کارتریج چاپگر',     36, '2026-05-03'),
  (3,  'central', 'صندلی اداری',       22, '2026-06-18'),
  (4,  'central', 'میز کار',           14, '2026-07-09'),
  (5,  'central', 'لپ‌تاپ',             9, '2026-08-27'),
  (6,  'central', 'مانیتور ۲۴ اینچ',   17, '2026-09-30'),
  (7,  'branch',  'کاغذ A4 (بسته)',   120, '2026-05-21'),
  (8,  'branch',  'کارتریج چاپگر',     11, '2026-06-02'),
  (9,  'branch',  'صندلی اداری',        8, '2026-07-15'),
  (10, 'branch',  'مانیتور ۲۴ اینچ',    5, '2026-09-11');

INSERT INTO order_lines VALUES
  (1, 1, 'کارتریج چاپگر', 5, 3250000.00),
  (2, 1, 'میز کار', 10, 6000000.00),
  (3, 2, 'صندلی اداری', 8, 5000000.00),
  (4, 2, 'لپ‌تاپ', 13, 7750000.00),
  (5, 2, 'کاغذ A4 (بسته)', 18, 10500000.00),
  (6, 3, 'میز کار', 11, 6750000.00),
  (7, 4, 'لپ‌تاپ', 14, 8500000.00),
  (8, 4, 'کاغذ A4 (بسته)', 2, 11250000.00),
  (9, 5, 'مانیتور ۲۴ اینچ', 17, 10250000.00),
  (10, 5, 'کارتریج چاپگر', 5, 3000000.00),
  (11, 5, 'میز کار', 10, 5750000.00),
  (12, 6, 'کاغذ A4 (بسته)', 3, 2000000.00),
  (13, 7, 'کارتریج چاپگر', 6, 3750000.00),
  (14, 7, 'میز کار', 11, 6500000.00),
  (15, 8, 'صندلی اداری', 9, 5500000.00),
  (16, 8, 'لپ‌تاپ', 14, 8250000.00),
  (17, 8, 'کاغذ A4 (بسته)', 2, 11000000.00),
  (18, 9, 'میز کار', 12, 7250000.00),
  (19, 10, 'لپ‌تاپ', 15, 9000000.00),
  (20, 10, 'کاغذ A4 (بسته)', 3, 1750000.00),
  (21, 11, 'مانیتور ۲۴ اینچ', 18, 10750000.00),
  (22, 11, 'کارتریج چاپگر', 6, 3500000.00),
  (23, 11, 'میز کار', 11, 6250000.00),
  (24, 12, 'کاغذ A4 (بسته)', 4, 2500000.00),
  (25, 13, 'کارتریج چاپگر', 7, 4250000.00),
  (26, 13, 'میز کار', 12, 7000000.00),
  (27, 14, 'صندلی اداری', 10, 6000000.00),
  (28, 14, 'لپ‌تاپ', 15, 8750000.00),
  (29, 14, 'کاغذ A4 (بسته)', 3, 1500000.00),
  (30, 15, 'میز کار', 13, 7750000.00),
  (31, 16, 'لپ‌تاپ', 16, 9500000.00),
  (32, 16, 'کاغذ A4 (بسته)', 4, 2250000.00),
  (33, 17, 'مانیتور ۲۴ اینچ', 2, 11250000.00),
  (34, 17, 'کارتریج چاپگر', 7, 4000000.00),
  (35, 17, 'میز کار', 12, 6750000.00),
  (36, 18, 'کاغذ A4 (بسته)', 5, 3000000.00);
