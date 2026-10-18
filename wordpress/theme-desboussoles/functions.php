<?php
/**
 * DesBoussoles — fonctions du thème.
 *
 * Thème bloc minimal : la charte (couleurs, typographie, formes) est portée
 * par theme.json. Ce fichier ajoute les supports d'édition, la feuille de
 * style et les styles de blocs réutilisables.
 *
 * @package theme-desboussoles
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

/**
 * Supports du thème + feuille de style front et éditeur.
 */
function theme_desboussoles_setup() {
	add_theme_support( 'post-thumbnails' );
	add_theme_support( 'responsive-embeds' );
	add_theme_support( 'editor-styles' );
	add_theme_support( 'html5', array( 'style', 'script' ) );
	add_editor_style( 'style.css' );
}
add_action( 'after_setup_theme', 'theme_desboussoles_setup' );

/**
 * Feuille de style du thème (compléments de theme.json).
 */
function theme_desboussoles_enqueue_styles() {
	wp_enqueue_style(
		'theme-desboussoles',
		get_stylesheet_uri(),
		array(),
		wp_get_theme()->get( 'Version' )
	);
}
add_action( 'wp_enqueue_scripts', 'theme_desboussoles_enqueue_styles' );

/**
 * Styles de blocs de la charte : carte et bandeau bordés.
 */
function theme_desboussoles_block_styles() {
	register_block_style(
		'core/group',
		array(
			'name'         => 'boussole-card',
			'label'        => __( 'Carte', 'theme-desboussoles' ),
			'inline_style' => '.wp-block-group.is-style-boussole-card{background:var(--wp--preset--color--blanc);border:3px solid var(--wp--preset--color--noir-doux);border-radius:16px;}',
		)
	);
	register_block_style(
		'core/group',
		array(
			'name'         => 'boussole-bandeau',
			'label'        => __( 'Bandeau', 'theme-desboussoles' ),
			'inline_style' => '.wp-block-group.is-style-boussole-bandeau{border:3px solid var(--wp--preset--color--noir-doux);border-radius:16px;}',
		)
	);
	register_block_style(
		'core/button',
		array(
			'name'         => 'boussole-urgence',
			'label'        => __( 'Urgence', 'theme-desboussoles' ),
			'inline_style' => '.wp-block-button.is-style-boussole-urgence .wp-block-button__link{background:var(--wp--preset--color--orange-urgence);color:var(--wp--preset--color--encre);}.wp-block-button.is-style-boussole-urgence .wp-block-button__link:hover{background:var(--wp--preset--color--jaune-vif);}',
		)
	);
}
add_action( 'init', 'theme_desboussoles_block_styles' );

/**
 * Longueur de l'extrait des articles.
 */
function theme_desboussoles_excerpt_length() {
	return 32;
}
add_filter( 'excerpt_length', 'theme_desboussoles_excerpt_length' );

/**
 * Points de suspension de l'extrait.
 */
function theme_desboussoles_excerpt_more() {
	return '…';
}
add_filter( 'excerpt_more', 'theme_desboussoles_excerpt_more' );
