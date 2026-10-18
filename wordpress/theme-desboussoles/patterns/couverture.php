<?php
/**
 * Title: Couverture
 * Slug: theme-desboussoles/couverture
 * Categories: featured, banner
 * Description: Grande couverture avec titre, texte et bouton.
 * Viewport Width: 1400
 */
?>
<!-- wp:group {"align":"full","className":"is-style-boussole-bandeau","backgroundColor":"fond-ciel","style":{"spacing":{"padding":{"top":"var:preset|spacing|xxl","bottom":"var:preset|spacing|xxl","left":"var:preset|spacing|lg","right":"var:preset|spacing|lg"}}},"layout":{"type":"constrained"}} -->
<div class="wp-block-group alignfull is-style-boussole-bandeau has-fond-ciel-background-color has-background" style="padding-top:var(--wp--preset--spacing--xxl);padding-right:var(--wp--preset--spacing--lg);padding-bottom:var(--wp--preset--spacing--xxl);padding-left:var(--wp--preset--spacing--lg)"><!-- wp:heading {"textAlign":"center","level":1,"style":{"typography":{"fontSize":"var:preset|font-size|display"}}} -->
<h1 class="wp-block-heading has-text-align-center" style="font-size:var(--wp--preset--font-size--display)"><?php esc_html_e( 'Un titre qui donne envie', 'theme-desboussoles' ); ?></h1>
<!-- /wp:heading -->

<!-- wp:paragraph {"align":"center","style":{"typography":{"fontSize":"var:preset|font-size|body-lg"}}} -->
<p class="has-text-align-center" style="font-size:var(--wp--preset--font-size--body-lg)"><?php esc_html_e( 'Un sous-titre chaleureux pour présenter votre page en une phrase.', 'theme-desboussoles' ); ?></p>
<!-- /wp:paragraph -->

<!-- wp:buttons {"layout":{"type":"flex","justifyContent":"center"}} -->
<div class="wp-block-buttons"><!-- wp:button -->
<div class="wp-block-button"><a class="wp-block-button__link wp-element-button"><?php esc_html_e( 'En savoir plus', 'theme-desboussoles' ); ?></a></div>
<!-- /wp:button -->

<!-- wp:button {"className":"is-style-outline"} -->
<div class="wp-block-button is-style-outline"><a class="wp-block-button__link wp-element-button"><?php esc_html_e( 'Nous contacter', 'theme-desboussoles' ); ?></a></div>
<!-- /wp:button --></div>
<!-- /wp:buttons --></div>
<!-- /wp:group -->
