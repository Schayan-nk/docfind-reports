-- id: sales_by_customer
-- title: فروش به تفکیک مشتری
-- description: جمع مبلغ و تعداد سفارش‌های هر مشتری در بازه‌ای که انتخاب می‌کنید
-- group: فروش
-- chart: doughnut | label=customer | value=total_amount
-- monthly: none | هر سطر این گزارش یک مشتری است، نه یک سفارش، و تاریخی ندارد که ماه‌به‌ماه شمرده شود.
-- total: order_count, total_amount
-- column: customer | مشتری
-- column: city | شهر
-- column: order_count | تعداد سفارش
-- column: total_amount | جمع مبلغ (ریال)
-- param: pcity | anyof | شهر
-- suggest: pcity | customers | city
-- param: pfrom | date | از تاریخ | default=year-start
-- param: pto | date | تا تاریخ | default=today
SELECT c.name        AS customer,
       c.city        AS city,
       COUNT(*)      AS order_count,
       SUM(o.amount) AS total_amount
FROM orders o
JOIN customers c ON c.id = o.customer_id
WHERE o.status <> 'cancelled'
  AND c.city REGEXP @pcity
  AND o.submitted_on BETWEEN @pfrom AND @pto
GROUP BY c.name, c.city
ORDER BY total_amount DESC
