type Name='brand'|'wall'|'watch'|'devices'|'accounts'|'logout'|'fullscreen'|'plus';
const paths:Record<Name,string>={
 brand:'M8 3H3v5m13-5h5v5M3 16v5h5m13-5v5h-5M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Z',
 wall:'M3 3h7v7H3zm11 0h7v7h-7zM3 14h7v7H3zm11 0h7v7h-7z',
 watch:'M3 5h18v14H3zm7 4 5 3-5 3z',
 devices:'M3 5h18v6H3zm0 8h18v6H3zm3-5h1m-1 8h1',
 accounts:'M16 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2m15-6a4 4 0 0 1 2 4v2M10 4a4 4 0 1 0 0 8 4 4 0 0 0 0-8Zm7 0a4 4 0 0 1 0 8',
 logout:'M10 4H4v16h6m4-12 4 4-4 4m-6-4h10',
 fullscreen:'M9 3H3v6m12-6h6v6M3 15v6h6m12-6v6h-6',
 plus:'M12 5v14M5 12h14',
};
export function Icon({name}:{name:Name}){
 return <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={paths[name]}/></svg>;
}

