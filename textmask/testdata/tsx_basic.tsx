import React from 'react';

// component comment with 'apostrophe
export function App(props: { title: string }) {
  const cls = `app ${props.title}`;
  return (
    <div className={cls}>
      <span title="a 'quoted' title">{props.title}</span>
      {/* jsx comment */}
    </div>
  );
}
