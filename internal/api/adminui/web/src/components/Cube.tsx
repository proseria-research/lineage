// The Lineage mark in three dimensions: a wireframe cube, slowly rotating. The brand mark is a
// thin-bordered square, so a cube is that same square extruded — the version page's ornament for
// the thing the page is about, a sealed immutable box of bytes.
//
// Six transparent faces with 1px currentColor borders, so it inherits the surrounding text color
// and flips correctly in light/dark like the rest of the monochrome system. Spin and the
// reduced-motion resting pose live in index.css.

const FACES = ["rotateY(0deg)", "rotateY(90deg)", "rotateY(180deg)", "rotateY(-90deg)", "rotateX(90deg)", "rotateX(-90deg)"];

export function Cube({ size = 22, className = "" }: { size?: number; className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={className}
      style={{ display: "inline-block", width: size, height: size, lineHeight: 0, verticalAlign: "middle", perspective: size * 4 }}
    >
      <span className="lineage-cube" style={{ display: "block", position: "relative", width: size, height: size }}>
        {FACES.map((r) => (
          <span
            key={r}
            style={{
              position: "absolute",
              inset: 0,
              boxSizing: "border-box",
              border: "1px solid currentColor",
              transform: `${r} translateZ(${size / 2}px)`,
            }}
          />
        ))}
      </span>
    </span>
  );
}
