-- id: stock_central
-- title: موجودی انبار مرکزی
-- description: موجودی هر کالا در انبار مرکزی و آخرین تغییر آن
-- group: انبار
-- family: warehouses | گزارش انبارها | انبار
-- variant: انبار مرکزی
-- chart: hbar | label=item | value=qty
-- monthly: updated_on | آخرین تغییر | calendar=gregorian
-- total: qty
-- column: item | کالا
-- column: qty | موجودی
-- column: updated_on | آخرین تغییر
-- param: pitem | like | کالا
-- suggest: pitem | stock | item
SELECT s.item                             AS item,
       s.qty                              AS qty,
       DATE_FORMAT(s.updated_at, '%Y-%m-%d') AS updated_on
FROM stock s
WHERE s.warehouse = 'central'
  AND s.item LIKE @pitem
ORDER BY s.item
