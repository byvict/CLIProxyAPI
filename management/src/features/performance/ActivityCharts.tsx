import { useState } from 'react';
import { calendarDays, compact, intensity, number, shortDate } from './metrics';
import styles from './Performance.module.scss';

interface CalendarProps {
  year: number;
  today: string;
  days: { date: string; value: number | null }[];
  unit: string;
}
export function ActivityCalendar({ year, today, days, unit }: CalendarProps) {
  const dates = calendarDays(year);
  const values = new Map(days.map((day) => [day.date, day.value]));
  const maximum = Math.max(0, ...days.map((day) => day.value ?? 0));
  const columns = dates.length / 7;
  const [selected, setSelected] = useState<string | null>(null);
  const description = (date: string) =>
    `${shortDate(date)}: ${date > today ? 'data futura' : values.get(date) == null ? 'sem dados' : `${number(values.get(date) ?? 0)} ${unit}`}`;
  return (
    <div className={styles.calendar}>
      <div className={styles.calendarScroll}>
        <svg
          viewBox={`0 0 ${columns * 13 + 28} 117`}
          role="img"
          aria-label={`Calendário de ${unit} em ${year}`}
        >
          {['S', 'Q', 'S'].map((label, index) => (
            <text key={index} x="2" y={39 + index * 26} className={styles.axisText}>
              {label}
            </text>
          ))}
          {Array.from({ length: 12 }, (_, month) => {
            const date = `${year}-${String(month + 1).padStart(2, '0')}-01`;
            const index = dates.indexOf(date);
            return (
              <text
                key={month}
                x={30 + Math.floor(index / 7) * 13}
                y="12"
                className={styles.axisText}
              >
                {['J', 'F', 'M', 'A', 'M', 'J', 'J', 'A', 'S', 'O', 'N', 'D'][month]}
              </text>
            );
          })}
          {dates.map(
            (date, index) =>
              date && (
                <circle
                  key={date}
                  cx={32 + Math.floor(index / 7) * 13}
                  cy={25 + (index % 7) * 13}
                  r="5.4"
                  className={styles.cell}
                  data-level={intensity(values.get(date) ?? 0, maximum)}
                  opacity={date > today ? 0.4 : 1}
                  onMouseEnter={() => setSelected(date)}
                  onMouseLeave={() => setSelected(null)}
                >
                  <title>{description(date)}</title>
                </circle>
              )
          )}
        </svg>
      </div>
      <div className={styles.calendarFooter}>
        <span aria-live="polite">{selected ? description(selected) : '\u00a0'}</span>
        <span className={styles.scale}>
          Menos{' '}
          {[0, 1, 2, 3, 4].map((level) => (
            <i key={level} className={styles.cell} data-level={level} />
          ))}{' '}
          Mais
        </span>
      </div>
    </div>
  );
}

export interface ChartPoint {
  date: string;
  value: number | null;
}
export function ActivityChart({
  points,
  unit,
  kind,
}: {
  points: ChartPoint[];
  unit: string;
  kind: 'area' | 'bars';
}) {
  const [selected, setSelected] = useState<number | null>(null);
  const width = 700;
  const height = 208;
  const maximum = Math.max(1, ...points.map((point) => point.value ?? 0));
  const step = width / Math.max(1, points.length);
  const x = (index: number) => step * (index + 0.5);
  const y = (value: number) => height - (value / maximum) * (height - 16);
  const segments: { path: string; first: number; last: number }[] = [];
  points.forEach((point, index) => {
    if (point.value === null) return;
    if (index === 0 || points[index - 1].value === null) {
      segments.push({ path: `M${x(index)},${y(point.value)}`, first: index, last: index });
    } else {
      const segment = segments[segments.length - 1];
      segment.path += ` L${x(index)},${y(point.value)}`;
      segment.last = index;
    }
  });
  const point = selected === null ? null : points[selected];
  const detail = point
    ? `${shortDate(point.date)} · ${point.value === null ? 'sem dados de tokens' : `${number(point.value)} ${unit}`}`
    : '';
  return (
    <div className={styles.chart}>
      <div className={styles.chartDetail} aria-live="polite">
        {detail || '\u00a0'}
      </div>
      <svg
        viewBox={`0 0 ${width} ${height + 1}`}
        role="img"
        tabIndex={0}
        aria-label={`${unit} nos últimos 30 dias. Use as setas para explorar.`}
        onKeyDown={(event) => {
          if (event.key === 'ArrowRight' || event.key === 'ArrowLeft') {
            event.preventDefault();
            setSelected((value) =>
              Math.max(
                0,
                Math.min(points.length - 1, (value ?? -1) + (event.key === 'ArrowRight' ? 1 : -1))
              )
            );
          }
        }}
        onBlur={() => setSelected(null)}
        onMouseLeave={() => setSelected(null)}
      >
        <line x1="0" y1={height} x2={width} y2={height} className={styles.baseline} />
        {kind === 'area'
          ? segments.map((segment) => (
              <g key={segment.first}>
                <path
                  d={`${segment.path} L${x(segment.last)},${height} L${x(segment.first)},${height} Z`}
                  className={styles.area}
                />
                <path d={segment.path} className={styles.line} />
              </g>
            ))
          : points.map(
              (item, index) =>
                item.value !== null && (
                  <rect
                    key={item.date}
                    x={index * step}
                    y={y(item.value)}
                    width={step - 1.5}
                    height={height - y(item.value)}
                    rx="1.5"
                    className={styles.bar}
                  />
                )
            )}
        {points.map((item, index) => (
          <rect
            key={item.date}
            x={index * step}
            y="0"
            width={step}
            height={height}
            fill="transparent"
            onMouseEnter={() => setSelected(index)}
          >
            <title>
              {shortDate(item.date)}:{' '}
              {item.value === null ? 'sem dados' : `${compact(item.value)} ${unit}`}
            </title>
          </rect>
        ))}
        {selected !== null && (
          <line x1={x(selected)} x2={x(selected)} y1="0" y2={height} className={styles.crosshair} />
        )}
      </svg>
      <div className={styles.chartDates}>
        <span>{shortDate(points[0].date)}</span>
        <span>Hoje</span>
      </div>
    </div>
  );
}
