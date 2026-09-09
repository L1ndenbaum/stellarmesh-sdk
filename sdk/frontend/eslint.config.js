import stylistic from '@stylistic/eslint-plugin';
import tseslint from 'typescript-eslint';

// 仅约束顶层独立声明，避免拆散函数内部语句、接口成员和入口重导出。
const topLevelDeclaration = {
  selector: [
    'Program > TSInterfaceDeclaration',
    'Program > TSTypeAliasDeclaration',
    'Program > FunctionDeclaration',
    'Program > ClassDeclaration',
    'Program > TSEnumDeclaration',
    'Program > ExportNamedDeclaration[declaration!=null]',
    'Program > ExportDefaultDeclaration',
  ].join(', '),
};

export default tseslint.config(
  { ignores: ['dist/**', 'node_modules/**'] },
  ...tseslint.configs.recommended,
  {
    plugins: { '@stylistic': stylistic },
    rules: {
      '@stylistic/padding-line-between-statements': [
        'error',
        { blankLine: 'always', prev: '*', next: topLevelDeclaration },
        { blankLine: 'always', prev: topLevelDeclaration, next: '*' },
      ],
    },
  },
);
