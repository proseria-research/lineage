/*
 * The example model the landing page is built around. Every drawing on the page — the hero's
 * version list, the fingerprint comparison — is seeded from these values, so the same version
 * looks the same wherever it appears.
 */

export type Stage = 'draft' | 'staging' | 'production' | 'archived';

export const STAGE_LABEL: Record<Stage, string> = {
	draft: 'Draft',
	staging: 'Staging',
	production: 'Production',
	archived: 'Archived',
};

/** The architecture is shared by every version below; only the weights move between them. */
const ARCH = {
	topology: 'sha256:11ab3f7c9d02e4a6b8c1d5f70392ae64bb17c2d9f04e6a8b1c3d5e7f90a2b4c6',
	shape: 'sha256:4c72e15a83b6d0f29e4c7a1b5d8f0326ae91c4d7b0e3f6a9c2d5e8b1f4a7c0d3',
	dtype: 'sha256:9e01d4b7a2c5f80e3b6d9a1c4f70e2b5d8a1c3f6b9e2d5a8c1f4b7e0d3a6c9f2',
};

export interface DemoVersion {
	name: string;
	stage: Stage;
	digest: string;
	hashes: { topology: string; shape: string; dtype: string; weights: string };
}

const v = (name: string, stage: Stage, digest: string, weights: string): DemoVersion => ({
	name,
	stage,
	digest,
	hashes: { ...ARCH, weights },
});

export const MODEL = 'fraud-detector';

export const VERSIONS: DemoVersion[] = [
	v('1.5.0', 'draft', 'c81e0a55', 'sha256:5d1b9e07c3a4f2816e0b7d93a5c1e4f7'),
	v('1.4.0', 'staging', '9f2c4a10', 'sha256:90ef6b3d0a7c4e19f2b5d8a1c4e7f0b3'),
	v('1.3.0', 'production', '4be7d2c9', 'sha256:2a7c90e4d1b35f68c0e9a2d7b4f1c836'),
	v('1.2.0', 'archived', '71d03f8a', 'sha256:e6b24c9f07a1d358b2e0c4f9a7d16b52'),
];

export const byName = (name: string) => VERSIONS.find((x) => x.name === name)!;

/** One block of a version's layer breakdown, as a producer reports it (`insight.layers`). */
export interface DemoLayer {
	ordinal: number;
	path: string;
	opType: string;
	repeatCount: number;
	shapeSignature: string;
	paramCount: number;
}

/** fraud-detector's layer breakdown: a small transformer over transaction sequences. */
export const LAYERS: DemoLayer[] = [
	{ ordinal: 0, path: 'embeddings.merchant', opType: 'Embedding', repeatCount: 1, shapeSignature: '[50000,256]', paramCount: 12_800_000 },
	{ ordinal: 1, path: 'encoder.layer.*.attention.q_proj', opType: 'Linear', repeatCount: 4, shapeSignature: '[256,256]', paramCount: 65_792 },
	{ ordinal: 2, path: 'encoder.layer.*.ffn.up', opType: 'Linear', repeatCount: 4, shapeSignature: '[256,1024]', paramCount: 263_168 },
	{ ordinal: 3, path: 'classifier', opType: 'Linear', repeatCount: 1, shapeSignature: '[256,2]', paramCount: 514 },
];
