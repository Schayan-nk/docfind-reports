-- id: orders
-- title: سفارش‌ها
-- description: همهٔ سفارش‌ها با مشتری، مسئول، وضعیت و مبلغ
-- group: فروش
-- chart: bar | label=owner_name | value=amount | agg=sum | normalise=person
-- monthly: submitted_on | تاریخ ثبت
-- person: owner_name
-- total: amount
-- column: order_no | شماره سفارش
-- column: customer | مشتری
-- column: owner_name | مسئول
-- column: status | وضعیت
-- column: submitted_on | تاریخ ثبت
-- column: amount | مبلغ (ریال)
-- param: pref | like | شماره سفارش
-- param: pcustomer | like | مشتری
-- suggest: pcustomer | customers | name
-- param: powner | like | مسئول
-- suggest: powner | staff | full_name | person
-- param: pstatus | choice | وضعیت
-- option: pstatus | open | باز
-- option: pstatus | shipped | ارسال‌شده
-- option: pstatus | cancelled | لغوشده
-- param: ptype | list | نوع سفارش | default=1,2
-- option: ptype | 1 | فروش داخلی
-- option: ptype | 2 | صادرات
-- param: pfrom | date | از تاریخ | default=year-start
-- param: pto | date | تا تاریخ | default=today
SELECT o.order_no     AS order_no,
       c.name         AS customer,
       o.owner_name   AS owner_name,
       o.status       AS status,
       o.submitted_on AS submitted_on,
       o.amount       AS amount
FROM orders o
JOIN customers c ON c.id = o.customer_id
WHERE o.order_no LIKE @pref
  AND c.name LIKE @pcustomer
  AND o.owner_name LIKE @powner
  AND o.status LIKE @pstatus
  AND o.order_type IN (@ptype)
  AND o.submitted_on BETWEEN @pfrom AND @pto
ORDER BY o.submitted_on DESC, o.order_no
