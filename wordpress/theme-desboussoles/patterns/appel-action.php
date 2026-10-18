<?php
/**
 * Title: Appel à l'action
 * Slug: theme-desboussoles/appel-action
 * Categories: call-to-action, buttons
 * Description: Bandeau d’appel à l’action avec deux boutons.
 * Viewport Width: 1400
 */
?>
<!-- wp:group {"align":"full","className":"is-style-boussole-bandeau","backgroundColor":"jaune-signature","style":{"spacing":{"padding":{"top":"var:preset|spacing|xl","bottom":"var:preset|spacing|xl","left":"var:preset|spacing|lg","right":"var:preset|spacing|lg"}}},"layout":{"type":"constrained"}} -->
<div class="wp-block-group alignfull is-style-boussole-bandeau has-jaune-signature-background-color has-background" style="padding-top:var(--wp--preset--spacing--xl);padding-right:var(--wp--preset--spacing--lg);padding-bottom:var(--wp--preset--spacing--xl);padding-left:var(--wp--preset--spacing--lg)"><!-- wp:heading {"textAlign":"center","level":2,"style":{"typography":{"fontSize":"var:preset|font-size|h2"}}} -->
<h2 class="wp-block-heading has-text-align-center" style="font-size:var(--wp--preset--font-size--h2)"><?php esc_html_e( 'Prêt à passer à l’action ?', 'theme-desboussoles' ); ?></h2>
<!-- /wp:heading -->

<!-- wp:paragraph {"align":"center"} -->
<p class="has-text-align-center"><?php esc_html_e( 'Une phrase courte pour inciter à cliquer.', 'theme-desboussoles' ); ?></p>
<!-- /wp:paragraph -->

<!-- wp:buttons {"layout":{"type":"flex","justifyContent":"center"}} -->
<div class="wp-block-buttons"><!-- wp:button -->
<div class="wp-block-button"><a class="wp-block-button__link wp-element-button"><?php esc_html_e( 'Commencer', 'theme-desboussoles' ); ?></a></div>
<!-- /wp:button -->

<!-- wp:button {"className":"is-style-boussole-urgence"} -->
<div class="wp-block-button is-style-boussole-urgence"><a class="wp-block-button__link wp-element-button"><?php esc_html_e( 'Besoin d’aide ?', 'theme-desboussoles' ); ?></a></div>
<!-- /wp:button --></div>
<!-- /wp:buttons --></div>
<!-- /wp:group -->
