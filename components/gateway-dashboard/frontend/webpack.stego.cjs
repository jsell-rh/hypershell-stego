const path = require('path');
const webpack = require('webpack');
const HtmlWebpackPlugin = require('html-webpack-plugin');
const MonacoWebpackPlugin = require('monaco-editor-webpack-plugin');
const MiniCssExtractPlugin = require('mini-css-extract-plugin');
const TerserPlugin = require('terser-webpack-plugin');
const packageJson = require('./package.json');

// Keep the upstream entry point and API. This production build emits local,
// bounded assets. It does not permit inline scripts or add external origins.
module.exports = {
  mode: 'production',
  entry: ['./src/editor.stego.ts', './src/index.tsx'],
  output: {
    path: path.resolve(__dirname, 'dist'),
    filename: 'assets/[name].[contenthash].js',
    chunkFilename: 'assets/[name].[contenthash].js',
    assetModuleFilename: 'assets/[contenthash][ext]',
    publicPath: '/',
    clean: true,
  },
  resolve: {
    extensions: ['.ts', '.tsx', '.js'],
    alias: {
      '~': path.resolve(__dirname, 'src'),
      '@stego/browser-client': path.resolve(__dirname, 'stego/browser-client/index.js'),
      '@stego/browser-dom': path.resolve(__dirname, 'stego/browser-dom/index.js'),
    },
  },
  module: {
    rules: [
      {
        test: /\.js$/,
        include: /node_modules[\\/]monaco-editor[\\/]esm[\\/]/,
        enforce: 'pre',
        loader: path.resolve(__dirname, 'stego/browser-dom/monaco-loader.cjs'),
      },
      {
        test: /\.tsx?$/,
        loader: 'ts-loader',
        exclude: /node_modules/,
        options: { transpileOnly: true },
      },
      { test: /\.css$/, use: [MiniCssExtractPlugin.loader, 'css-loader'] },
      { test: /\.(woff2?|ttf|eot|svg|png|jpg|gif)$/, type: 'asset/resource' },
    ],
  },
  plugins: [
    new webpack.DefinePlugin({ __APP_VERSION__: JSON.stringify(packageJson.version) }),
    new HtmlWebpackPlugin({ template: './public/index.stego.html' }),
    new MiniCssExtractPlugin({ filename: 'assets/[name].[contenthash].css', chunkFilename: 'assets/[name].[contenthash].css' }),
    new MonacoWebpackPlugin({ languages: ['yaml', 'json'], filename: 'assets/[name].[contenthash].worker.js' }),
  ],
  optimization: {
    runtimeChunk: 'single',
    splitChunks: { chunks: 'all', minSize: 64 * 1024, maxSize: 512 * 1024 },
    minimizer: [new TerserPlugin({
      parallel: 1,
      extractComments: false,
      terserOptions: { format: { comments: /@license|@preserve|^!/ } },
    })],
  },
  devtool: false,
  performance: { hints: 'error', maxAssetSize: 4 * 1024 * 1024, maxEntrypointSize: 8 * 1024 * 1024 },
};
