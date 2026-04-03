import {Redirect} from '@docusaurus/router';
import useBaseUrl from '@docusaurus/useBaseUrl';

export default function Home() {
  const to = useBaseUrl('/next/intro');
  return <Redirect to={to} />;
}

